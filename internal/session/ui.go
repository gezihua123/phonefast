package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// getUIConn returns the persistent UI socket connection, creating it lazily.
// On error the stale connection is dropped so the next call re-dials.
func (s *Session) getUIConn() (net.Conn, error) {
	if s.uiPort == 0 {
		return nil, fmt.Errorf("ui socket not configured")
	}
	if s.uiConn != nil {
		return s.uiConn, nil
	}
	// Dial the loopback address directly. "localhost" would go through the
	// system resolver (mDNSResponder on macOS under CGO builds), which has
	// transiently failed for minutes under daemon restart churn — NXDOMAIN
	// for a name that never needs resolving. The adb forward binds
	// 127.0.0.1, so the literal IP removes the failure class entirely.
	conn, err := net.DialTimeout("tcp",
		fmt.Sprintf("127.0.0.1:%d", s.uiPort), 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect ui socket: %w", err)
	}
	s.uiConn = conn
	return conn, nil
}

// dropUIConn closes and discards the persistent UI connection.
// The next getUIConn() call will create a fresh one.
func (s *Session) dropUIConn() {
	if s.uiConn != nil {
		s.uiConn.Close()
		s.uiConn = nil
	}
}

// GetUISummary retrieves UI hierarchy in summary mode via the fast UI socket.
// Summary mode filters out layout containers on the server side.
// maxElements controls the element limit. Pass <= 0 for server default (DefMaxElements).
func (s *Session) GetUISummary(maxElements int) ([]protocol.UIElement, error) {
	elems, err := s.getUISummaryOnce(maxElements)
	if err == nil {
		return elems, nil
	}
	// Retry once on transient errors (stale-node exceptions during animations
	// are common now that waitForIdle is removed) — the same mitigation
	// ObserveFull applies to GetUIFull.
	return s.getUISummaryOnce(maxElements)
}

func (s *Session) getUISummaryOnce(maxElements int) ([]protocol.UIElement, error) {
	conn, err := s.getUIConn()
	if err != nil {
		return nil, err
	}

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if err := protocol.WriteUISummaryRequest(conn, maxElements); err != nil {
		s.dropUIConn()
		return nil, fmt.Errorf("write ui summary request: %w", err)
	}

	resp, err := protocol.ReadUIDumpResponse(conn)
	if err != nil {
		s.dropUIConn()
		return nil, fmt.Errorf("read ui summary response: %w", err)
	}
	if resp.Error != "" {
		s.dropUIConn()
		return nil, fmt.Errorf("server error: %s", resp.Error)
	}

	// Client-side truncation (server always returns its default max)
	if maxElements > 0 && len(resp.Elements) > maxElements {
		resp.Elements = resp.Elements[:maxElements]
	}
	return resp.Elements, nil
}

// GetUIFull retrieves the complete UI hierarchy with parent/depth metadata via the fast UI socket.
// This mode returns ALL nodes (no filtering) for generating hierarchical formats
// (jsonl, simplexml, flatref).
// maxElements controls the element limit. Pass <= 0 for server default (DefMaxElements).
func (s *Session) GetUIFull(maxElements int) ([]protocol.UIFullElement, error) {
	if s.getUIFullFn != nil { // test seam — see Session fields
		return s.getUIFullFn(maxElements)
	}
	conn, err := s.getUIConn()
	if err != nil {
		return nil, err
	}

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if err := protocol.WriteUIFullRequest(conn, maxElements); err != nil {
		s.dropUIConn()
		return nil, fmt.Errorf("write ui full request: %w", err)
	}

	resp, err := protocol.ReadUIFullResponse(conn)
	if err != nil {
		s.dropUIConn()
		return nil, fmt.Errorf("read ui full response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("server error: %s", resp.Error)
	}

	if maxElements > 0 && len(resp.Elements) > maxElements {
		resp.Elements = resp.Elements[:maxElements]
	}
	return resp.Elements, nil
}

// GetUIElementsFallbackADB retrieves UI hierarchy via traditional ADB uiautomator dump.
// This is used when the fast UI socket is unavailable.
// maxElements is accepted for API consistency (ADB-side limit not supported).
func (s *Session) GetUIElementsFallbackADB(maxElements int) ([]protocol.UIElement, error) {
	xmlContent, err := dumpUIXML(s.serial)
	if err != nil {
		return nil, fmt.Errorf("adb ui dump: %w", err)
	}

	elements, err := parseUIXML(xmlContent)
	if err != nil {
		return nil, err
	}
	_ = maxElements // ADB fallback doesn't support server-side limiting
	return elements, nil
}

// uiFallbackBudget bounds the WHOLE adb uiautomator fallback chain (dump +
// retry + cat). The daemon's actor executes requests synchronously with a
// 60s reply window and does not cancel the work when the reply timer fires,
// so the chain's worst case must stay well under that window: the fast-path
// retries burn up to ~10s first, leaving ~40s for the fallback. Per-command
// adbShellTimeout (30s) is too coarse alone — three 30s commands would
// exceed the window.
const uiFallbackBudget = 40 * time.Second

// dumpUIXML runs uiautomator dump via ADB and returns the XML content.
// Each command is bounded by the remaining fallback budget, so the whole
// chain fits the daemon's actor reply window (see uiFallbackBudget).
func dumpUIXML(serial string) (string, error) {
	dumpPath := "/sdcard/phonefast_ui_dump.xml"
	deadline := time.Now().Add(uiFallbackBudget)
	run := func(args ...string) (string, error) {
		remain := time.Until(deadline)
		if remain <= 0 {
			return "", context.DeadlineExceeded
		}
		return adb.ADBShellTimeout(serial, remain, args...)
	}

	// Run uiautomator dump
	_, err := run("uiautomator", "dump", dumpPath)
	if err != nil {
		// Fallback: try with --window-animation-disabled. A hang
		// (DeadlineExceeded) is NOT a transient uiautomator failure —
		// retrying it with a fresh bound just doubles the wait. Only retry
		// real errors, per the "surface hangs, don't retry" policy.
		//
		// Before retrying, give the device a beat to settle: during daemon
		// (re)start churn the device-side lmkd SIGKILLs uiautomator dumps
		// (observed as exit 137 for the first minutes of a stress run); an
		// immediate retry lands in the same kill window and fails the same
		// way. The settle delay is negligible next to the dump's own cost.
		if !errors.Is(err, context.DeadlineExceeded) {
			time.Sleep(500 * time.Millisecond)
			_, err = run("uiautomator", "dump", "--window-animation-disabled", dumpPath)
		}
		if err != nil {
			return "", fmt.Errorf("uiautomator dump: %w", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	// Read the dumped XML
	xmlContent, err := run("cat", dumpPath)
	if err != nil {
		return "", fmt.Errorf("read ui dump: %w", err)
	}

	return xmlContent, nil
}

// parseUIXML parses uiautomator XML output into UIElement list.
func parseUIXML(xml string) ([]protocol.UIElement, error) {
	var elements []protocol.UIElement

	// Simple state-machine XML parser for uiautomator output.
	// Avoids importing encoding/xml for speed and to handle malformed XML.
	//
	// We look for <node ... /> tags and extract attributes.

	lines := strings.Split(xml, "<node")
	index := 0
	for _, line := range lines[1:] { // skip content before first <node
		el := parseNodeLine("<node" + line)
		if el == nil {
			continue
		}
		el.Index = index
		elements = append(elements, *el)
		index++
	}

	return elements, nil
}

func parseNodeLine(line string) *protocol.UIElement {
	// Scan past the opening <node to find the tag end, skipping quoted values.
	// This is needed because resource-ids contain "/" (e.g., "com.app:id/btn")
	// which would otherwise break IndexAny(">/").
	inQuote := false
	quoteChar := byte(0)
	end := -1
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inQuote {
			if c == quoteChar {
				inQuote = false
			}
		} else {
			if c == '"' || c == '\'' {
				inQuote = true
				quoteChar = c
			} else if c == '>' || c == '/' {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return nil
	}
	attrs := line[:end]

	el := &protocol.UIElement{
		Enabled: true,
	}

	// Parse bounds
	if b := extractAttr(attrs, "bounds"); b != "" {
		fmt.Sscanf(b, "[%d,%d][%d,%d]",
			&el.Bounds[0], &el.Bounds[1],
			&el.Bounds[2], &el.Bounds[3])
		el.Center[0] = (el.Bounds[0] + el.Bounds[2]) / 2
		el.Center[1] = (el.Bounds[1] + el.Bounds[3]) / 2
	}

	// Only include elements with valid bounds
	if el.Bounds[2] <= el.Bounds[0] || el.Bounds[3] <= el.Bounds[1] {
		return nil
	}

	el.Text = extractAttr(attrs, "text")
	el.ContentDesc = extractAttr(attrs, "content-desc")
	el.ResourceID = extractAttr(attrs, "resource-id")
	el.ClassName = extractAttr(attrs, "class")
	el.Clickable = extractAttr(attrs, "clickable") == "true"
	el.Enabled = extractAttr(attrs, "enabled") != "false" // default true
	el.Focused = extractAttr(attrs, "focused") == "true"
	el.Selected = extractAttr(attrs, "selected") == "true"

	// Filter: skip elements with no identifier and not clickable
	if !el.Clickable && el.Text == "" && el.ContentDesc == "" && el.ResourceID == "" {
		return nil
	}

	return el
}

func extractAttr(s, name string) string {
	// Search for name=" and name=' variants. Enforce a word boundary before the
	// attribute name (preceded by space/tab or at string start) to avoid matching
	// a suffix of a longer attribute name (e.g. "id" matching inside "resource-id").
	for _, q := range []byte{'"', '\''} {
		prefix := name + "=" + string(q)
		offset := 0
		for {
			idx := strings.Index(s[offset:], prefix)
			if idx < 0 {
				break
			}
			abs := offset + idx
			if abs > 0 && s[abs-1] != ' ' && s[abs-1] != '\t' {
				// Not a word boundary — keep searching past this position
				offset = abs + 1
				continue
			}
			valStart := abs + len(prefix)
			end := strings.IndexByte(s[valStart:], q)
			if end < 0 {
				return strings.TrimSpace(s[valStart:])
			}
			return strings.TrimSpace(s[valStart : valStart+end])
		}
	}
	return ""
}
