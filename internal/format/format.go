// Package format provides shared formatting utilities used across the
// daemon RPC handler, MCP server, and CLI direct mode — ensuring all
// three output paths produce identical UI element text.
package format

import (
	"fmt"
	"strings"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// ElementsForLLM formats the given UI elements into a human-readable string
// suitable for LLM consumption or CLI display.
//
// Parameters:
//   - elements: the UI elements to format (may be nil/empty)
//   - maxShow: max number of elements to display (< 0 = all, 0 = default 100)
//   - isSummary: if true, use simplified class names and filter layout containers
func ElementsForLLM(elements []protocol.UIElement, maxShow int, isSummary bool) string {
	if len(elements) == 0 {
		return "No interactive elements found on screen."
	}

	if maxShow < 0 || maxShow > len(elements) {
		maxShow = len(elements)
	}

	var lines []string
	lines = append(lines, "Interactive elements on screen:")
	lines = append(lines, strings.Repeat("=", 50))

	for i, el := range elements {
		if i >= maxShow {
			lines = append(lines, fmt.Sprintf("... and %d more elements", len(elements)-maxShow))
			break
		}

		if isSummary && protocol.IsLayoutClass(el.ClassName) && !el.Clickable && el.Text == "" && el.ContentDesc == "" {
			maxShow++ // don't count this filtered element
			continue
		}

		var parts []string
		if el.Text != "" {
			parts = append(parts, fmt.Sprintf(`text="%s"`, el.Text))
		}
		if el.ContentDesc != "" {
			parts = append(parts, fmt.Sprintf(`desc="%s"`, el.ContentDesc))
		}
		if el.ResourceID != "" {
			simpleID := el.ResourceID
			if idx := strings.LastIndexByte(simpleID, '/'); idx >= 0 {
				simpleID = simpleID[idx+1:]
			}
			parts = append(parts, fmt.Sprintf(`id="%s"`, simpleID))
		}
		if el.ClassName != "" {
			var cn string
			if isSummary {
				cn = protocol.SimplifyClassName(el.ClassName)
			} else {
				cn = el.ClassName
				if idx := strings.LastIndexByte(cn, '.'); idx >= 0 {
					cn = cn[idx+1:]
				}
			}
			parts = append(parts, fmt.Sprintf("(%s)", cn))
		}
		if el.Clickable {
			parts = append(parts, "[clickable]")
		}

		desc := strings.Join(parts, " ")
		if desc == "" {
			desc = fmt.Sprintf("(%s)", el.ClassName)
		}
		lines = append(lines, fmt.Sprintf("[%d] %s bounds=[%d,%d][%d,%d]",
			el.Index, desc,
			el.Bounds[0], el.Bounds[1], el.Bounds[2], el.Bounds[3]))
	}

	lines = append(lines, strings.Repeat("=", 50))
	lines = append(lines, "Use tap_element with index=N or text='...' to interact.")

	return strings.Join(lines, "\n")
}
