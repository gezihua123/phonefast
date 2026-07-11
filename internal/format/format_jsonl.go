package format

import (
	"fmt"
	"strings"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// jsonlFormatter implements UIFormatter for JSON Lines format.
// This is the highest-accuracy format for LLM consumption (98.5% in benchmarks).
//
// Example output:
//
//	{"id": 19, "parent": 17, "depth": 16, "clickable": true, "bounds": "[857,399][1017,525]"}
//	{"id": 20, "parent": 19, "depth": 17, "content_desc": "安装", "bounds": "[899,432][975,491]"}
type jsonlFormatter struct{}

func init() {
	register(&jsonlFormatter{})
}

func (f *jsonlFormatter) Name() string { return "jsonl" }

func (f *jsonlFormatter) Format(elements []protocol.UIFullElement) string {
	if len(elements) == 0 {
		return "No UI elements found on screen."
	}

	var b strings.Builder
	for _, el := range elements {
		b.WriteString(`{"id":`)
		b.WriteString(fmt.Sprintf("%d", el.ID))
		b.WriteString(`,"parent":`)
		b.WriteString(fmt.Sprintf("%d", el.Parent))
		b.WriteString(`,"depth":`)
		b.WriteString(fmt.Sprintf("%d", el.Depth))

		if el.Text != "" {
			b.WriteString(`,"text":`)
			writeJSONString(&b, el.Text)
		}
		if el.ContentDesc != "" {
			b.WriteString(`,"content_desc":`)
			writeJSONString(&b, el.ContentDesc)
		}
		if el.ResourceID != "" {
			b.WriteString(`,"resource_id":`)
			writeJSONString(&b, el.ResourceID)
		}
		if el.ClassName != "" {
			b.WriteString(`,"class":`)
			writeJSONString(&b, simplifyClassName(el.ClassName))
		}

		b.WriteString(`,"bounds":"`)
		b.WriteString(formatBounds(el.Bounds))
		b.WriteByte('"')

		b.WriteString(`,"clickable":`)
		if el.Clickable {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}

		if !el.Enabled {
			b.WriteString(`,"enabled":false`)
		}
		if el.Focused {
			b.WriteString(`,"focused":true`)
		}
		if el.Selected {
			b.WriteString(`,"selected":true`)
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// ElementsToJSONL is a convenience wrapper for backward compatibility.
func ElementsToJSONL(elements []protocol.UIFullElement) string {
	return (&jsonlFormatter{}).Format(elements)
}
