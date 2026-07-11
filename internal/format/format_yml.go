package format

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// ymlFormatter implements UIFormatter for YAML-like hierarchical format.
// Preserves parent-child nesting through indentation. Includes all attributes.
//
// Example output:
//
//	- id: 0
//	  class: FrameLayout
//	  bounds: [0,0][1080,2194]
//	  children:
//	    - id: 1
//	      text: Settings
//	      clickable: true
type ymlFormatter struct{}

func init() {
	register(&ymlFormatter{})
}

func (f *ymlFormatter) Name() string { return "yml" }

func (f *ymlFormatter) Format(elements []protocol.UIFullElement) string {
	if len(elements) == 0 {
		return "No UI elements found on screen."
	}

	root := buildTree(elements)

	var b ymlBuffer
	writeYMLNode(&b, root, 0)
	return b.String()
}

// ymlBuffer is a strings.Builder with an indent helper.
type ymlBuffer struct {
	strings.Builder
}

func (b *ymlBuffer) writeIndent(depth int) {
	for i := 0; i < depth; i++ {
		b.WriteString("  ")
	}
}

func writeYMLNode(b *ymlBuffer, node *uiNode, depth int) {
	if node == nil || node.element == nil {
		return
	}
	el := node.element

	b.writeIndent(depth)
	b.WriteString("- id: ")
	b.WriteString(fmt.Sprintf("%d", el.ID))
	b.WriteByte('\n')

	b.writeIndent(depth + 1)
	b.WriteString("class: ")
	if el.ClassName != "" {
		b.WriteString(simplifyClassName(el.ClassName))
	}
	b.WriteByte('\n')

	if el.Text != "" {
		b.writeIndent(depth + 1)
		b.WriteString("text: '")
		b.WriteString(strings.ReplaceAll(el.Text, "'", "''"))
		b.WriteString("'\n")
	}
	if el.ContentDesc != "" {
		b.writeIndent(depth + 1)
		b.WriteString("content_desc: '")
		b.WriteString(strings.ReplaceAll(el.ContentDesc, "'", "''"))
		b.WriteString("'\n")
	}
	if el.ResourceID != "" {
		b.writeIndent(depth + 1)
		b.WriteString("resource_id: ")
		b.WriteString(el.ResourceID)
		b.WriteByte('\n')
	}

	b.writeIndent(depth + 1)
	b.WriteString("clickable: ")
	if el.Clickable {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteByte('\n')

	b.writeIndent(depth + 1)
	b.WriteString("bounds: '")
	b.WriteString(formatBounds(el.Bounds))
	b.WriteString("'\n")

	if len(node.children) > 0 {
		b.writeIndent(depth + 1)
		b.WriteString("children:\n")
		// Sort children by ID for consistent output
		sort.Slice(node.children, func(i, j int) bool {
			return node.children[i].element.ID < node.children[j].element.ID
		})
		for _, child := range node.children {
			writeYMLNode(b, child, depth+2)
		}
	}
}

// ElementsToYML is a convenience wrapper for backward compatibility.
func ElementsToYML(elements []protocol.UIFullElement) string {
	return (&ymlFormatter{}).Format(elements)
}
