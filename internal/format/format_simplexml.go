package format

import (
	"fmt"
	"strings"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// simpleXMLFormatter implements UIFormatter for simplified XML format.
// Only non-empty/non-default attributes are included. Class names are shortened
// to simple form (e.g. "android.widget.TextView" → "TextView").
//
// Example output:
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<node index="1" class="View" bounds="[857,399][1017,525]" clickable="True">
//	  <node class="View" content-desc="安装" bounds="[899,432][975,491]" />
//	</node>
type simpleXMLFormatter struct{}

func init() {
	register(&simpleXMLFormatter{})
}

func (f *simpleXMLFormatter) Name() string { return "simplexml" }

func (f *simpleXMLFormatter) Format(elements []protocol.UIFullElement) string {
	if len(elements) == 0 {
		return "No UI elements found on screen."
	}

	root := buildTree(elements)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteByte('\n')
	writeSimpleXMLNode(&b, root, 0)
	return b.String()
}

// writeSimpleXMLNode recursively writes a node and its children as simplified XML.
func writeSimpleXMLNode(b *strings.Builder, node *uiNode, indent int) {
	if node == nil || node.element == nil {
		return
	}
	el := node.element

	// Indent
	for i := 0; i < indent; i++ {
		b.WriteString("  ")
	}

	b.WriteString("<node")

	// index attribute for sibling disambiguation
	b.WriteString(` index="`)
	b.WriteString(fmt.Sprintf("%d", el.ID))
	b.WriteByte('"')

	// class (simple name)
	if el.ClassName != "" {
		b.WriteString(` class="`)
		b.WriteString(xmlEscape(simplifyClassName(el.ClassName)))
		b.WriteByte('"')
	}

	if el.Text != "" {
		b.WriteString(` text="`)
		b.WriteString(xmlEscape(el.Text))
		b.WriteByte('"')
	}
	if el.ContentDesc != "" {
		b.WriteString(` content-desc="`)
		b.WriteString(xmlEscape(el.ContentDesc))
		b.WriteByte('"')
	}
	if el.ResourceID != "" {
		b.WriteString(` resource-id="`)
		b.WriteString(xmlEscape(el.ResourceID))
		b.WriteByte('"')
	}

	b.WriteString(` bounds="`)
	b.WriteString(formatBounds(el.Bounds))
	b.WriteByte('"')

	if el.Clickable {
		b.WriteString(` clickable="True"`)
	}
	if !el.Enabled {
		b.WriteString(` enabled="False"`)
	}
	if el.Focused {
		b.WriteString(` focused="True"`)
	}
	if el.Selected {
		b.WriteString(` selected="True"`)
	}

	if len(node.children) == 0 {
		b.WriteString(" />\n")
	} else {
		b.WriteString(">\n")
		for _, child := range node.children {
			writeSimpleXMLNode(b, child, indent+1)
		}
		for i := 0; i < indent; i++ {
			b.WriteString("  ")
		}
		b.WriteString("</node>\n")
	}
}

// ElementsToSimpleXML is a convenience wrapper for backward compatibility.
func ElementsToSimpleXML(elements []protocol.UIFullElement) string {
	return (&simpleXMLFormatter{}).Format(elements)
}
