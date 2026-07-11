package format

import (
	"fmt"
	"strings"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// flatRefFormatter implements UIFormatter for flat reference format.
// Each line is self-contained with explicit parent references.
// Four semantic groups separated by | for unambiguous LLM parsing:
//
//	#ID <identity> | bounds=[...] | [flags] | depth=N parent=#M
//
// Example output:
//
//	#19 (View) | bounds=[857,399][1017,525] | [clickable] | depth=16 parent=#17
//	#20 desc="安装" (View) | bounds=[899,432][975,491] | | depth=17 parent=#19
type flatRefFormatter struct{}

func init() {
	register(&flatRefFormatter{})
}

func (f *flatRefFormatter) Name() string { return "flatref" }

func (f *flatRefFormatter) Format(elements []protocol.UIFullElement) string {
	if len(elements) == 0 {
		return "No UI elements found on screen."
	}

	var b strings.Builder
	for i, el := range elements {
		b.WriteString("#")
		b.WriteString(fmt.Sprintf("%d", el.ID))

		// --- identity ---
		if el.Text != "" {
			b.WriteString(` text="`)
			b.WriteString(sanitizeFlatRefValue(el.Text))
			b.WriteByte('"')
		}
		if el.ContentDesc != "" {
			b.WriteString(` desc="`)
			b.WriteString(sanitizeFlatRefValue(el.ContentDesc))
			b.WriteByte('"')
		}
		if el.ResourceID != "" {
			b.WriteString(` id="`)
			b.WriteString(simplifyResourceID(el.ResourceID))
			b.WriteByte('"')
		}
		if el.ClassName != "" {
			b.WriteString(" (")
			b.WriteString(simplifyClassName(el.ClassName))
			b.WriteByte(')')
		}

		// --- bounds ---
		b.WriteString(" | bounds=[")
		b.WriteString(formatBoundsCompact(el.Bounds))
		b.WriteString("]")

		// --- interactive state (space-separated within group) ---
		b.WriteString(" |")
		if el.Clickable {
			b.WriteString(" [clickable]")
		}
		if el.Focused {
			b.WriteString(" [focused]")
		}
		if el.Selected {
			b.WriteString(" [selected]")
		}
		if !el.Enabled {
			b.WriteString(" [disabled]")
		}

		// --- tree metadata ---
		b.WriteString(" | depth=")
		b.WriteString(fmt.Sprintf("%d", el.Depth))
		b.WriteString(" parent=#")
		b.WriteString(fmt.Sprintf("%d", el.Parent))

		if i < len(elements)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ElementsToFlatRef is a convenience wrapper for backward compatibility.
func ElementsToFlatRef(elements []protocol.UIFullElement) string {
	return (&flatRefFormatter{}).Format(elements)
}
