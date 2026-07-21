// Package format provides shared formatting utilities used across the
// daemon RPC handler, MCP server, and CLI direct mode — ensuring all
// output paths produce identical UI element text.
//
// Each hierarchical format (jsonl, simplexml, flatref, yml) is implemented
// in its own file as a UIFormatter, registered in the global FormatRegistry.
package format

import (
	"fmt"
	"strings"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// Ensure format files are imported for init() registration.
// Each format_*.go file calls register() in its init().
var _ = FormatRegistry // reference to suppress "unused" during incremental builds

// UIFormatter converts a full element hierarchy into a formatted string.
// Each format (jsonl, simplexml, flatref, yml) implements this interface.
type UIFormatter interface {
	// Name returns the format identifier (e.g. "jsonl", "simplexml").
	Name() string

	// Format converts the full element hierarchy into the format's text output.
	Format(elements []protocol.UIFullElement) string
}

// FormatRegistry maps format names to their UIFormatter implementations.
// Populated by each format file's init().
var FormatRegistry = map[string]UIFormatter{}

// register adds a formatter to the global registry.
func register(f UIFormatter) {
	FormatRegistry[f.Name()] = f
}

// ByName returns the UIFormatter for the given format name, or nil if unknown.
func ByName(name string) UIFormatter {
	return FormatRegistry[name]
}

// FormatNames returns the list of registered format names.
func FormatNames() []string {
	names := make([]string, 0, len(FormatRegistry))
	for name := range FormatRegistry {
		names = append(names, name)
	}
	return names
}

// ── Legacy flat format (for non-hierarchical dump/sum modes) ─────────────

// ElementsForLLM formats flat UI elements into a human-readable string
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
			parts = append(parts, fmt.Sprintf(`text="%s"`, CollapseWS(el.Text)))
		}
		if el.ContentDesc != "" {
			parts = append(parts, fmt.Sprintf(`desc="%s"`, CollapseWS(el.ContentDesc)))
		}
		if el.ResourceID != "" && !isObfuscatedID(el.ResourceID) {
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

// ElementsForLLMWithViewport is like ElementsForLLM but collapses elements
// outside the visible viewport (screenW × screenH) into a single summary line.
// Pass screenW/screenH = 0 to disable off-screen collapsing.
// Learned from agent-device's visible-first trimming: off-screen interactive
// items are collapsed to "[off-screen] N items: ..." instead of listed fully.
func ElementsForLLMWithViewport(elements []protocol.UIElement, maxShow int, isSummary bool, screenW, screenH int) string {
	if len(elements) == 0 {
		return "No interactive elements found on screen."
	}

	hasViewport := screenW > 0 && screenH > 0
	var onScreen []protocol.UIElement
	var offScreenInteractive []protocol.UIElement
	for _, el := range elements {
		if hasViewport && isOffScreen(el.Bounds, screenW, screenH) {
			if el.Text != "" || el.ContentDesc != "" || el.Clickable {
				offScreenInteractive = append(offScreenInteractive, el)
			}
			continue
		}
		onScreen = append(onScreen, el)
	}

	totalCount := len(elements)
	visibleCount := len(onScreen)
	if maxShow < 0 || maxShow > visibleCount {
		maxShow = visibleCount
	}

	var lines []string
	header := "Interactive elements on screen"
	if hasViewport {
		header += fmt.Sprintf(" (%d visible of %d total, %dx%d)", visibleCount, totalCount, screenW, screenH)
	} else if visibleCount < totalCount {
		header += fmt.Sprintf(" (%d of %d)", visibleCount, totalCount)
	}
	lines = append(lines, header+":")
	lines = append(lines, strings.Repeat("=", 50))

	shown := 0
	for _, el := range onScreen {
		if shown >= maxShow {
			lines = append(lines, fmt.Sprintf("... and %d more visible elements", visibleCount-maxShow))
			break
		}
		if isSummary && protocol.IsLayoutClass(el.ClassName) && !el.Clickable && el.Text == "" && el.ContentDesc == "" {
			continue
		}
		line := formatElementLine(el, isSummary)
		if line != "" {
			lines = append(lines, line)
			shown++
		}
	}

	if len(offScreenInteractive) > 0 {
		lines = append(lines, formatOffScreenSummary(offScreenInteractive))
	}

	lines = append(lines, strings.Repeat("=", 50))
	lines = append(lines, "Use tap_element with index=N or text='...' to interact.")
	return strings.Join(lines, "\n")
}

// isOffScreen reports whether bounds lie entirely beyond the viewport.
func isOffScreen(b [4]int, screenW, screenH int) bool {
	if b[2] <= 0 || b[3] <= 0 {
		return true
	}
	if b[0] >= screenW || b[1] >= screenH {
		return true
	}
	return false
}

// formatElementLine renders a single element line, skipping obfuscated IDs.
func formatElementLine(el protocol.UIElement, isSummary bool) string {
	var parts []string
	if el.Text != "" {
		parts = append(parts, fmt.Sprintf(`text="%s"`, CollapseWS(el.Text)))
	}
	if el.ContentDesc != "" {
		parts = append(parts, fmt.Sprintf(`desc="%s"`, CollapseWS(el.ContentDesc)))
	}
	if el.ResourceID != "" && !isObfuscatedID(el.ResourceID) {
		parts = append(parts, fmt.Sprintf(`id="%s"`, simplifyResourceID(el.ResourceID)))
	}
	if el.ClassName != "" {
		cn := el.ClassName
		if isSummary {
			cn = protocol.SimplifyClassName(el.ClassName)
		} else if idx := strings.LastIndexByte(cn, '.'); idx >= 0 {
			cn = cn[idx+1:]
		}
		parts = append(parts, fmt.Sprintf("(%s)", cn))
	}
	if el.Clickable {
		parts = append(parts, "[clickable]")
	}
	desc := strings.Join(parts, " ")
	if desc == "" {
		return ""
	}
	return fmt.Sprintf("[%d] %s bounds=[%d,%d][%d,%d]",
		el.Index, desc,
		el.Bounds[0], el.Bounds[1], el.Bounds[2], el.Bounds[3])
}

// formatOffScreenSummary collapses off-screen interactive elements into a
// compact discovery line, capping the number of labels listed.
func formatOffScreenSummary(elems []protocol.UIElement) string {
	var labels []string
	for _, el := range elems {
		lbl := el.Text
		if lbl == "" {
			lbl = el.ContentDesc
		}
		if lbl != "" {
			labels = append(labels, fmt.Sprintf("%q", CollapseWS(lbl)))
			if len(labels) >= 5 {
				labels = append(labels, "...")
				break
			}
		}
	}
	if len(labels) > 0 {
		return fmt.Sprintf("[off-screen] %d interactive items: %s", len(elems), strings.Join(labels, ", "))
	}
	return fmt.Sprintf("[off-screen] %d interactive items", len(elems))
}

// CompactElements filters out redundant layout containers from the element list.
// A container is redundant if it's a known layout class AND has no semantic
// properties (no text, content-desc, resource-id, and not clickable/focused/selected/disabled).
// Children of removed nodes are re-parented to their nearest surviving ancestor.
// Root elements (parent < 0) are never removed.
func CompactElements(elements []protocol.UIFullElement) []protocol.UIFullElement {
	if len(elements) <= 1 {
		return elements
	}

	// Build ID → index map for fast lookup
	idToIdx := make(map[int]int, len(elements))
	for i := range elements {
		idToIdx[elements[i].ID] = i
	}

	// Mark redundant elements
	redundant := make(map[int]bool, len(elements))
	for i := range elements {
		el := &elements[i]
		// Root is never redundant
		if el.Parent < 0 {
			continue
		}
		// Must be a known layout class
		if !protocol.IsLayoutClass(el.ClassName) {
			continue
		}
		// Must have no semantic properties
		if el.Text != "" || el.ContentDesc != "" || el.ResourceID != "" {
			continue
		}
		if el.Clickable || el.Focused || el.Selected || !el.Enabled {
			continue
		}
		redundant[el.ID] = true
	}

	// Quick check: if nothing to remove, return original
	if len(redundant) == 0 {
		return elements
	}

	// Find nearest surviving ancestor for a given element ID (with memoization)
	ancestorCache := make(map[int]int)
	var findAncestor func(id int) int
	findAncestor = func(id int) int {
		if cached, ok := ancestorCache[id]; ok {
			return cached
		}
		if id < 0 {
			return id
		}
		if !redundant[id] {
			ancestorCache[id] = id
			return id
		}
		// Walk up to parent
		if idx, ok := idToIdx[id]; ok {
			parentID := elements[idx].Parent
			ancestor := findAncestor(parentID)
			ancestorCache[id] = ancestor
			return ancestor
		}
		ancestorCache[id] = id
		return id
	}

	// Precompute compacted depth for surviving elements.
	// Walk the original parent chain, counting only non-redundant ancestors.
	compactedDepth := make(map[int]int)
	var calcDepth func(id int) int
	calcDepth = func(id int) int {
		if id < 0 {
			return -1 // sentinel: depth before the root
		}
		if d, ok := compactedDepth[id]; ok {
			return d
		}
		idx, ok := idToIdx[id]
		if !ok {
			return 0
		}
		parentDepth := calcDepth(elements[idx].Parent)
		if redundant[id] {
			compactedDepth[id] = parentDepth // skipped, inherit parent's depth
		} else {
			compactedDepth[id] = parentDepth + 1
		}
		return compactedDepth[id]
	}
	// Pre-calculate for all surviving elements
	for i := range elements {
		if !redundant[elements[i].ID] {
			calcDepth(elements[i].ID)
		}
	}

	// Build result
	result := make([]protocol.UIFullElement, 0, len(elements)-len(redundant))
	for i := range elements {
		el := elements[i]
		if redundant[el.ID] {
			continue
		}

		// Re-parent to nearest surviving ancestor
		if el.Parent >= 0 && redundant[el.Parent] {
			el.Parent = findAncestor(el.Parent)
		}

		el.Depth = compactedDepth[el.ID]

		result = append(result, el)
	}

	return result
}

// ── Shared tree / utility types ─────────────────────────────────────────

// uiNode is a tree node used to reconstruct hierarchy for tree-based formats.
type uiNode struct {
	element  *protocol.UIFullElement
	children []*uiNode
}

// buildTree reconstructs a tree from a flat DFS-ordered element array.
// Uses the ID field (not array index) for parent-child linking.
// Orphan nodes (whose parent doesn't exist) are attached to the first root.
func buildTree(elements []protocol.UIFullElement) *uiNode {
	if len(elements) == 0 {
		return &uiNode{}
	}

	// Create nodes and build an ID→node map
	nodes := make([]*uiNode, len(elements))
	idToIndex := make(map[int]int, len(elements))
	for i := range elements {
		nodes[i] = &uiNode{element: &elements[i]}
		idToIndex[elements[i].ID] = i
	}

	// Find the first root (parent == -1)
	firstRootIdx := -1
	for i, el := range elements {
		if el.Parent < 0 {
			firstRootIdx = i
			break
		}
	}
	if firstRootIdx < 0 {
		firstRootIdx = 0 // fallback: use first element as root
	}

	// Link children to parents using element ID (not array index)
	// Elements are DFS-ordered, so parent always comes before children.
	for i, el := range elements {
		if el.Parent < 0 {
			continue
		}
		if parentIdx, ok := idToIndex[el.Parent]; ok {
			nodes[parentIdx].children = append(nodes[parentIdx].children, nodes[i])
		} else {
			// Orphan: parent doesn't exist — attach to first root
			nodes[firstRootIdx].children = append(nodes[firstRootIdx].children, nodes[i])
		}
	}

	return nodes[firstRootIdx]
}

// simplifyClassName returns the simple class name (last segment after '.').
func simplifyClassName(fullName string) string {
	if idx := strings.LastIndexByte(fullName, '.'); idx >= 0 {
		return fullName[idx+1:]
	}
	return fullName
}

// simplifyResourceID returns the short resource ID (after last '/').
func simplifyResourceID(fullID string) string {
	if idx := strings.LastIndexByte(fullID, '/'); idx >= 0 {
		return fullID[idx+1:]
	}
	return fullID
}

// IsObfuscatedID reports whether a resource ID is a meaningless obfuscated
// identifier (e.g. "0_resource_name_obfuscated") that only wastes tokens.
// Learned from agent-device: skip IDs that carry no semantic value.
func IsObfuscatedID(id string) bool {
	simple := simplifyResourceID(id)
	return strings.Contains(simple, "resource_name_obfuscated") ||
		strings.HasPrefix(simple, "0_resource")
}

// isObfuscatedID is the unexported alias for internal use.
func isObfuscatedID(id string) bool { return IsObfuscatedID(id) }

// ── String escaping utilities (shared across formats) ───────────────────

// writeJSONString writes a JSON-escaped string to the builder.
func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04x`, r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// xmlEscape escapes special XML characters in attribute values.
// Control characters (below 0x20 except tab/newline) are replaced with spaces.
func xmlEscape(s string) string {
	var b strings.Builder
	needsEscape := false
	for i, r := range s {
		switch {
		case r == '&':
			if !needsEscape {
				needsEscape = true
				b.Grow(len(s) + 32)
				b.WriteString(s[:i])
			}
			b.WriteString("&amp;")
		case r == '"':
			if !needsEscape {
				needsEscape = true
				b.Grow(len(s) + 32)
				b.WriteString(s[:i])
			}
			b.WriteString("&quot;")
		case r == '<':
			if !needsEscape {
				needsEscape = true
				b.Grow(len(s) + 32)
				b.WriteString(s[:i])
			}
			b.WriteString("&lt;")
		case r == '>':
			if !needsEscape {
				needsEscape = true
				b.Grow(len(s) + 32)
				b.WriteString(s[:i])
			}
			b.WriteString("&gt;")
		case r == '\'':
			if !needsEscape {
				needsEscape = true
				b.Grow(len(s) + 32)
				b.WriteString(s[:i])
			}
			b.WriteString("&apos;")
		case r < 0x20 && r != '\t' && r != '\n':
			if !needsEscape {
				needsEscape = true
				b.Grow(len(s) + 32)
				b.WriteString(s[:i])
			}
			b.WriteByte(' ')
		default:
			if needsEscape {
				b.WriteRune(r)
			}
		}
	}
	if !needsEscape {
		return s
	}
	return b.String()
}

// sanitizeFlatRefValue replaces control characters and newlines with a space
// so each flatref line remains self-contained and safe for line-based parsing.
func sanitizeFlatRefValue(s string) string {
	return CollapseWS(s)
}

// CollapseWS collapses all whitespace runs (newlines, tabs, spaces) into a
// single space. Also strips leading/trailing whitespace and control characters.
// Used to sanitize multi-line content descriptions and text before display.
func CollapseWS(s string) string {
	// Fast path: check if any sanitization is needed
	needsFix := false
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b == 0x7F {
			needsFix = true
			break
		}
	}
	if !needsFix {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	inWS := true // start true to skip leading whitespace
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7F || c == ' ' {
			if !inWS {
				b.WriteByte(' ')
				inWS = true
			}
		} else {
			b.WriteByte(c)
			inWS = false
		}
	}
	// Trim trailing space
	res := b.String()
	if len(res) > 0 && res[len(res)-1] == ' ' {
		res = res[:len(res)-1]
	}
	return res
}

// ── Bounds formatting ────────────────────────────────────────────────────

// formatBounds returns a string like "[left,top][right,bottom]".
func formatBounds(b [4]int) string {
	return fmt.Sprintf("[%d,%d][%d,%d]", b[0], b[1], b[2], b[3])
}

// formatBoundsCompact returns a string like "left,top][right,bottom" (no leading '[').
func formatBoundsCompact(b [4]int) string {
	return fmt.Sprintf("%d,%d][%d,%d", b[0], b[1], b[2], b[3])
}
