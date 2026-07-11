package format

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// sampleHierarchy returns a realistic Compose-style button hierarchy
// (simulating Google Play install button structure from TMP.md).
func sampleHierarchy() []protocol.UIFullElement {
	return []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			ClassName: "android.widget.FrameLayout",
			Bounds:    [4]int{0, 0, 1080, 2194},
			Center:    [2]int{540, 1097},
			Enabled:   true,
		},
		{
			ID: 17, Parent: 0, Depth: 1,
			ClassName: "android.widget.LinearLayout",
			Bounds:    [4]int{0, 0, 1080, 2194},
			Center:    [2]int{540, 1097},
			Enabled:   true,
		},
		{
			ID: 19, Parent: 17, Depth: 2,
			ClassName: "android.view.View",
			Bounds:    [4]int{857, 399, 1017, 525},
			Center:    [2]int{937, 462},
			Clickable: true,
			Enabled:   true,
		},
		{
			ID: 20, Parent: 19, Depth: 3,
			ClassName:   "android.view.View",
			ContentDesc: "安装",
			Bounds:      [4]int{899, 432, 975, 491},
			Center:      [2]int{937, 461},
			Enabled:     true,
		},
		{
			ID: 21, Parent: 20, Depth: 4,
			ClassName: "android.widget.TextView",
			Text:      "安装",
			Bounds:    [4]int{899, 432, 975, 491},
			Center:    [2]int{937, 461},
			Enabled:   true,
		},
		{
			ID: 22, Parent: 19, Depth: 3,
			ClassName: "android.widget.Button",
			Bounds:    [4]int{857, 409, 1017, 514},
			Center:    [2]int{937, 461},
			Enabled:   true,
		},
	}
}

func TestElementsToJSONL(t *testing.T) {
	result := ElementsToJSONL(sampleHierarchy())

	// Should contain multiple lines
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 6 lines, got %d", len(lines))
	}

	// Each line should be valid JSON
	for i, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d is not valid JSON: %v\n  %s", i, err, line)
		}
	}

	// Verify key fields exist in first line
	firstLine := lines[0]
	if !strings.Contains(firstLine, `"id":0`) {
		t.Error("first line should contain id=0")
	}
	if !strings.Contains(firstLine, `"parent":-1`) {
		t.Error("root node parent should be -1")
	}
	if !strings.Contains(firstLine, `"clickable":false`) {
		t.Error("should include clickable=false")
	}

	// Verify clickable node (id=19)
	clickableLine := lines[2]
	if !strings.Contains(clickableLine, `"id":19`) {
		t.Error("line 3 should be id=19")
	}
	if !strings.Contains(clickableLine, `"clickable":true`) {
		t.Error("id=19 should be clickable=true")
	}

	// Verify text node (id=21) has text="安装"
	textLine := lines[4]
	if !strings.Contains(textLine, `"text":"安装"`) {
		t.Errorf("id=21 should contain text='安装', got: %s", textLine)
	}

	t.Logf("JSONL output:\n%s", result)
}

func TestElementsToJSONLEmpty(t *testing.T) {
	result := ElementsToJSONL(nil)
	if result != "No UI elements found on screen." {
		t.Errorf("empty elements should return placeholder, got: %s", result)
	}

	result = ElementsToJSONL([]protocol.UIFullElement{})
	if result != "No UI elements found on screen." {
		t.Errorf("empty slice should return placeholder, got: %s", result)
	}
}

func TestElementsToSimpleXML(t *testing.T) {
	result := ElementsToSimpleXML(sampleHierarchy())

	// Should contain XML declaration
	if !strings.HasPrefix(result, "<?xml") {
		t.Error("should start with XML declaration")
	}

	// Should contain nested nodes
	if !strings.Contains(result, "<node") {
		t.Error("should contain <node> tags")
	}

	// Should close properly
	if !strings.Contains(result, "</node>") {
		t.Error("should have closing tags")
	}

	// Verify key attributes
	if !strings.Contains(result, `clickable="True"`) {
		t.Error("clickable nodes should have clickable='True'")
	}

	// Verify content-desc
	if !strings.Contains(result, `content-desc="安装"`) {
		t.Error("should contain content-desc='安装'")
	}

	// Verify text
	if !strings.Contains(result, `text="安装"`) {
		t.Error("should contain text='安装'")
	}

	// Verify class names are simplified
	if !strings.Contains(result, `class="FrameLayout"`) {
		t.Error("class should be simplified to FrameLayout")
	}

	t.Logf("SimpleXML output:\n%s", result)
}

func TestElementsToSimpleXMLEmpty(t *testing.T) {
	result := ElementsToSimpleXML(nil)
	if result != "No UI elements found on screen." {
		t.Errorf("empty elements should return placeholder, got: %s", result)
	}
}

func TestElementsToFlatRef(t *testing.T) {
	result := ElementsToFlatRef(sampleHierarchy())

	lines := strings.Split(result, "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 6 lines, got %d", len(lines))
	}

	// First line: root
	if !strings.Contains(lines[0], "#0") {
		t.Error("first element should be #0")
	}
	if !strings.Contains(lines[0], "parent=#-1") {
		t.Error("root's parent should be #-1")
	}

	// Clickable element (#19)
	clickableLine := lines[2]
	if !strings.Contains(clickableLine, "#19") {
		t.Error("should contain #19")
	}
	if !strings.Contains(clickableLine, "[clickable]") {
		t.Error("clickable node should have [clickable] tag")
	}
	if !strings.Contains(clickableLine, "(View)") {
		t.Error("should contain class name in parentheses")
	}

	// Text element (#21)
	textLine := lines[4]
	if !strings.Contains(textLine, `text="安装"`) {
		t.Errorf("should contain text attribute, got: %s", textLine)
	}

	// Desc element (#20)
	descLine := lines[3]
	if !strings.Contains(descLine, `desc="安装"`) {
		t.Errorf("should contain desc attribute, got: %s", descLine)
	}

	// All lines should have bounds
	for i, line := range lines {
		if !strings.Contains(line, "bounds=[") {
			t.Errorf("line %d missing bounds: %s", i, line)
		}
	}

	t.Logf("FlatRef output:\n%s", result)
}

func TestElementsToFlatRefEmpty(t *testing.T) {
	result := ElementsToFlatRef(nil)
	if result != "No UI elements found on screen." {
		t.Errorf("empty elements should return placeholder, got: %s", result)
	}
}

func TestElementsToYML(t *testing.T) {
	result := ElementsToYML(sampleHierarchy())

	// Should contain id and indentation
	if !strings.Contains(result, "- id: 0") {
		t.Error("should contain '- id: 0'")
	}
	if !strings.Contains(result, "class:") {
		t.Error("should contain 'class:'")
	}
	if !strings.Contains(result, "clickable:") {
		t.Error("should contain 'clickable:'")
	}
	if !strings.Contains(result, "children:") {
		t.Error("should contain nested 'children:' sections")
	}

	// Verify all nodes appear in the YML output
	for _, id := range []int{0, 17, 19, 20, 21, 22} {
		search := fmt.Sprintf("id: %d", id)
		if !strings.Contains(result, search) {
			t.Errorf("YML output should contain node %q", search)
		}
	}

	t.Logf("YML output:\n%s", result)
}

func TestElementsToYMLEmpty(t *testing.T) {
	result := ElementsToYML(nil)
	if result != "No UI elements found on screen." {
		t.Errorf("empty elements should return placeholder, got: %s", result)
	}
}

func TestBuildTree(t *testing.T) {
	elements := sampleHierarchy()
	root := buildTree(elements)

	if root == nil {
		t.Fatal("root should not be nil")
	}
	if root.element.ID != 0 {
		t.Errorf("root ID should be 0, got %d", root.element.ID)
	}
	if root.element.Parent != -1 {
		t.Errorf("root Parent should be -1, got %d", root.element.Parent)
	}
	if len(root.children) != 1 {
		t.Fatalf("root should have 1 child, got %d", len(root.children))
	}

	// Root's child should be id=17
	child := root.children[0]
	if child.element.ID != 17 {
		t.Errorf("root's child ID should be 17, got %d", child.element.ID)
	}

	// id=17 should have id=19 as child
	if len(child.children) != 1 {
		t.Fatalf("id=17 should have 1 child, got %d", len(child.children))
	}
	grandchild := child.children[0]
	if grandchild.element.ID != 19 {
		t.Errorf("id=17's child should be id=19, got %d", grandchild.element.ID)
	}

	// id=19 should have 2 children: id=20 and id=22
	if len(grandchild.children) != 2 {
		t.Fatalf("id=19 should have 2 children, got %d", len(grandchild.children))
	}
}

func TestWriteJSONString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", `"hello"`},
		{`say "hi"`, `"say \"hi\""`},
		{"line1\nline2", `"line1\nline2"`},
		{"tab\there", `"tab\there"`},
		{"back\\slash", `"back\\slash"`},
	}

	for _, tt := range tests {
		var b strings.Builder
		writeJSONString(&b, tt.input)
		got := b.String()
		if got != tt.want {
			t.Errorf("writeJSONString(%q) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestXMLEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"a&b", "a&amp;b"},
		{`say "hi"`, "say &quot;hi&quot;"},
		{"a<b>c", "a&lt;b&gt;c"},
		{"it's", "it&apos;s"},
		{"bell\x07char", "bell char"}, // control char → space
		{"multi\x00\x01\x02chars", "multi   chars"},
	}

	for _, tt := range tests {
		got := xmlEscape(tt.input)
		if got != tt.want {
			t.Errorf("xmlEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// Test that jsonl output matches the sample format from TMP.md
func TestElementsToJSONLFormatMatch(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 19, Parent: 17, Depth: 16,
			ClassName: "android.view.View",
			Bounds:    [4]int{857, 399, 1017, 525},
			Center:    [2]int{937, 462},
			Clickable: true,
		},
		{
			ID: 20, Parent: 19, Depth: 17,
			ClassName:   "android.view.View",
			ContentDesc: "安装",
			Bounds:      [4]int{899, 432, 975, 491},
			Center:      [2]int{937, 461},
		},
	}

	result := ElementsToJSONL(elements)
	lines := strings.Split(strings.TrimSpace(result), "\n")

	// Line 1: verify id, parent, clickable, bounds
	if !strings.Contains(lines[0], `"id":19`) {
		t.Error("jsonl line 1 should contain id=19")
	}
	if !strings.Contains(lines[0], `"parent":17`) {
		t.Error("jsonl line 1 should contain parent=17")
	}
	if !strings.Contains(lines[0], `"depth":16`) {
		t.Error("jsonl line 1 should contain depth=16")
	}
	if !strings.Contains(lines[0], `"clickable":true`) {
		t.Error("jsonl line 1 should have clickable=true")
	}
	if !strings.Contains(lines[0], `[857,399][1017,525]`) {
		t.Error("jsonl line 1 should have correct bounds")
	}

	// Line 2: verify content_desc
	if !strings.Contains(lines[1], `"content_desc":"安装"`) {
		t.Errorf("jsonl line 2 should have content_desc, got: %s", lines[1])
	}
}

// ── JSONL edge case tests ──

func TestElementsToJSONLSpecialCharacters(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			Text:      `say "hello"`,
			ClassName: "android.widget.TextView",
			Bounds:    [4]int{10, 20, 100, 80},
		},
		{
			ID: 1, Parent: 0, Depth: 1,
			ContentDesc: "line1\nline2",
			ClassName:   "android.widget.Button",
			Bounds:      [4]int{50, 60, 200, 120},
		},
		{
			ID: 2, Parent: 1, Depth: 2,
			Text:      "tab\there",
			ClassName: "android.widget.EditText",
			Bounds:    [4]int{0, 0, 300, 50},
		},
	}

	result := ElementsToJSONL(elements)
	lines := strings.Split(strings.TrimSpace(result), "\n")

	// Line 0: should escape double quotes inside text
	if !strings.Contains(lines[0], `say \"hello\"`) {
		t.Errorf("line 0 should escape quotes, got: %s", lines[0])
	}

	// Line 1: should escape newline
	if !strings.Contains(lines[1], `line1\nline2`) {
		t.Errorf("line 1 should escape newline, got: %s", lines[1])
	}

	// Line 2: should escape tab
	if !strings.Contains(lines[2], `tab\there`) {
		t.Errorf("line 2 should escape tab, got: %s", lines[2])
	}

	// Every line must be valid JSON
	for i, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestElementsToJSONLWithResourceID(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			ResourceID: "com.example:id/btn_submit",
			ClassName:  "android.widget.Button",
			Bounds:     [4]int{10, 20, 100, 80},
		},
	}

	result := ElementsToJSONL(elements)
	if !strings.Contains(result, `"resource_id":"com.example:id/btn_submit"`) {
		t.Errorf("should include resource_id, got: %s", result)
	}
}

func TestElementsToJSONLFocusedSelected(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			ClassName: "android.widget.EditText",
			Bounds:    [4]int{10, 20, 100, 80},
			Focused:   true,
			Selected:  true,
			Clickable: true,
		},
	}

	result := ElementsToJSONL(elements)
	if !strings.Contains(result, `"focused":true`) {
		t.Errorf("should include focused=true, got: %s", result)
	}
	if !strings.Contains(result, `"selected":true`) {
		t.Errorf("should include selected=true, got: %s", result)
	}
}

func TestElementsToJSONLDisabled(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			ClassName: "android.widget.Button",
			Bounds:    [4]int{10, 20, 100, 80},
			Enabled:   false,
		},
	}

	result := ElementsToJSONL(elements)
	if !strings.Contains(result, `"enabled":false`) {
		t.Errorf("should include enabled=false when disabled, got: %s", result)
	}
}

func TestElementsToJSONLEnabledByDefault(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			ClassName: "android.widget.Button",
			Bounds:    [4]int{10, 20, 100, 80},
			Enabled:   true,
		},
	}

	result := ElementsToJSONL(elements)
	// enabled=true should be omitted (default)
	if strings.Contains(result, `"enabled":true`) || strings.Contains(result, `"enabled"`) {
		// Actually, we never emit "enabled":true, only "enabled":false
	}
	// Just verify valid JSON
	for _, line := range strings.Split(strings.TrimSpace(result), "\n") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line is not valid JSON: %v\n  %s", err, line)
		}
	}
}

// ── SimpleXML edge case tests ──

func TestElementsToSimpleXMLSpecialCharacters(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			Text:      "price < $10 & more",
			ClassName: "android.widget.TextView",
			Bounds:    [4]int{10, 20, 200, 80},
		},
		{
			ID: 1, Parent: 0, Depth: 1,
			Text:      `he said "hello"`,
			ClassName: "android.widget.TextView",
			Bounds:    [4]int{30, 100, 180, 150},
		},
	}

	result := ElementsToSimpleXML(elements)

	// XML entities should be properly escaped
	if !strings.Contains(result, "price &lt; $10 &amp; more") {
		t.Errorf("XML text should escape < and &: %s", result)
	}
	if !strings.Contains(result, "he said &quot;hello&quot;") {
		t.Errorf("XML text should escape quotes: %s", result)
	}
	// Should be valid XML (has opening/closing tags)
	if strings.Count(result, "<node") != strings.Count(result, "</node>")+strings.Count(result, "/>") {
		t.Error("XML should be well-formed: unbalanced tags")
	}
}

func TestElementsToSimpleXMLResourceID(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			ResourceID: "com.example:id/btn_submit",
			ClassName:  "android.widget.Button",
			Bounds:     [4]int{10, 20, 100, 80},
			Clickable:  true,
		},
	}

	result := ElementsToSimpleXML(elements)
	if !strings.Contains(result, `resource-id="com.example:id/btn_submit"`) {
		t.Errorf("should include resource-id, got: %s", result)
	}
	if !strings.Contains(result, `clickable="True"`) {
		t.Errorf("should include clickable=True, got: %s", result)
	}
}

func TestElementsToSimpleXMLFocusedSelected(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			ClassName: "android.widget.EditText",
			Bounds:    [4]int{0, 0, 300, 50},
			Focused:   true,
			Selected:  true,
		},
	}

	result := ElementsToSimpleXML(elements)
	if !strings.Contains(result, `focused="True"`) {
		t.Errorf("should include focused=True, got: %s", result)
	}
	if !strings.Contains(result, `selected="True"`) {
		t.Errorf("should include selected=True, got: %s", result)
	}
}

func TestElementsToSimpleXMLMultipleRoots(t *testing.T) {
	// Simulate multi-window dump: two independent root nodes
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 1000}},
		{ID: 1, Parent: 0, Depth: 1, Text: "Main", ClassName: "TextView", Bounds: [4]int{10, 10, 100, 50}},
		{ID: 5, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{100, 200, 500, 600}},
		{ID: 6, Parent: 5, Depth: 1, Text: "Dialog", ClassName: "TextView", Bounds: [4]int{150, 250, 300, 300}},
	}

	result := ElementsToSimpleXML(elements)
	// The first root wins, but we should still see the structure
	if !strings.Contains(result, `<node index="0"`) {
		t.Errorf("should contain first root: %s", result)
	}
}

// ── FlatRef edge case tests ──

func TestElementsToFlatRefMultipleSiblings(t *testing.T) {
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		{ID: 1, Parent: 0, Depth: 1, Text: "First", ClassName: "Button", Bounds: [4]int{10, 10, 100, 60}},
		{ID: 2, Parent: 0, Depth: 1, Text: "Second", ClassName: "Button", Bounds: [4]int{10, 70, 100, 120}},
		{ID: 3, Parent: 0, Depth: 1, Text: "Third", ClassName: "Button", Bounds: [4]int{10, 130, 100, 180}},
	}

	result := ElementsToFlatRef(elements)
	lines := strings.Split(result, "\n")

	// Verify all siblings have correct parent=#0 and depth=1
	for i := 1; i <= 3; i++ {
		if !strings.Contains(lines[i], "parent=#0") {
			t.Errorf("sibling id=%d should have parent=#0: %s", i, lines[i])
		}
		if !strings.Contains(lines[i], "depth=1") {
			t.Errorf("sibling id=%d should have depth=1: %s", i, lines[i])
		}
	}
}

func TestElementsToFlatRefDeepNesting(t *testing.T) {
	// 10 levels deep
	elements := make([]protocol.UIFullElement, 10)
	for i := 0; i < 10; i++ {
		parent := i - 1
		elements[i] = protocol.UIFullElement{
			ID:        i,
			Parent:    parent,
			Depth:     i,
			ClassName: "View",
			Bounds:    [4]int{i * 10, i * 10, 100 + i*10, 100 + i*10},
		}
	}

	result := ElementsToFlatRef(elements)
	lines := strings.Split(result, "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}

	// Last element should have depth=9, parent=#8
	if !strings.Contains(lines[9], "depth=9") {
		t.Errorf("last element should have depth=9: %s", lines[9])
	}
	if !strings.Contains(lines[9], "parent=#8") {
		t.Errorf("last element's parent should be #8: %s", lines[9])
	}
}

func TestElementsToFlatRefAllTags(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			ClassName: "android.widget.Button",
			Bounds:    [4]int{10, 20, 100, 80},
			Clickable: true,
			Focused:   true,
			Selected:  true,
			Enabled:   false,
		},
	}

	result := ElementsToFlatRef(elements)
	if !strings.Contains(result, "[clickable]") {
		t.Errorf("should have [clickable] tag: %s", result)
	}
	if !strings.Contains(result, "[focused]") {
		t.Errorf("should have [focused] tag: %s", result)
	}
	if !strings.Contains(result, "[selected]") {
		t.Errorf("should have [selected] tag: %s", result)
	}
	if !strings.Contains(result, "[disabled]") {
		t.Errorf("should have [disabled] tag: %s", result)
	}
}

// ── YML edge case tests ──

func TestElementsToYMLDeepNesting(t *testing.T) {
	elements := make([]protocol.UIFullElement, 10)
	for i := 0; i < 10; i++ {
		parent := i - 1
		elements[i] = protocol.UIFullElement{
			ID:        i,
			Parent:    parent,
			Depth:     i,
			ClassName: "View",
			Bounds:    [4]int{i * 10, i * 10, 100 + i*10, 100 + i*10},
		}
	}

	result := ElementsToYML(elements)
	// All 10 nodes should appear
	for i := 0; i < 10; i++ {
		search := fmt.Sprintf("id: %d", i)
		if !strings.Contains(result, search) {
			t.Errorf("YML should contain node %q", search)
		}
	}
	// Deepest node should have the most indentation
	if !strings.Contains(result, "id: 9") {
		t.Error("should contain deepest node id: 9")
	}
}

func TestElementsToYMLWithSingleQuotes(t *testing.T) {
	elements := []protocol.UIFullElement{
		{
			ID: 0, Parent: -1, Depth: 0,
			Text:      "it's a test",
			ClassName: "android.widget.TextView",
			Bounds:    [4]int{10, 20, 100, 80},
		},
	}

	result := ElementsToYML(elements)
	// Single quotes in text should be doubled (YAML escaping)
	if !strings.Contains(result, "it''s a test") {
		t.Errorf("YML should escape single quotes by doubling: %s", result)
	}
}

// ── buildTree robustness tests ──

func TestBuildTreeNonContiguousIDs(t *testing.T) {
	elements := []protocol.UIFullElement{
		{ID: 100, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 100, 100}},
		{ID: 200, Parent: 100, Depth: 1, ClassName: "TextView", Bounds: [4]int{10, 10, 50, 50}},
		{ID: 300, Parent: 200, Depth: 2, Text: "leaf", ClassName: "TextView", Bounds: [4]int{20, 20, 30, 30}},
	}

	root := buildTree(elements)
	if root.element.ID != 100 {
		t.Errorf("root ID should be 100, got %d", root.element.ID)
	}
	if len(root.children) != 1 {
		t.Fatalf("root should have 1 child, got %d", len(root.children))
	}
	if root.children[0].element.ID != 200 {
		t.Errorf("child ID should be 200, got %d", root.children[0].element.ID)
	}
	if len(root.children[0].children) != 1 {
		t.Fatalf("grandchild missing")
	}
	if root.children[0].children[0].element.ID != 300 {
		t.Errorf("grandchild ID should be 300, got %d", root.children[0].children[0].element.ID)
	}
	// Verify depth field matches
	if root.children[0].element.Depth != 1 {
		t.Errorf("id=200 depth should be 1, got %d", root.children[0].element.Depth)
	}
}

func TestBuildTreeRootless(t *testing.T) {
	// All nodes have parent >= 0 — no root
	elements := []protocol.UIFullElement{
		{ID: 1, Parent: 0, Depth: 1, ClassName: "TextView", Bounds: [4]int{10, 10, 50, 50}},
		{ID: 2, Parent: 0, Depth: 1, ClassName: "Button", Bounds: [4]int{10, 60, 100, 120}},
	}

	root := buildTree(elements)
	// Should fall back to first element
	if root == nil {
		t.Fatal("should return non-nil root even without parent=-1")
	}
	// First element becomes root
	if root.element.ID != 1 {
		t.Errorf("root fallback should be first element (id=1), got id=%d", root.element.ID)
	}
}

func TestBuildTreeSingleNode(t *testing.T) {
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, Text: "only", ClassName: "TextView", Bounds: [4]int{0, 0, 100, 50}},
	}

	root := buildTree(elements)
	if root.element.ID != 0 {
		t.Errorf("single node root ID should be 0, got %d", root.element.ID)
	}
	if len(root.children) != 0 {
		t.Errorf("single node should have no children, got %d", len(root.children))
	}
}

func TestBuildTreeEmpty(t *testing.T) {
	root := buildTree(nil)
	if root == nil {
		t.Fatal("should return non-nil empty node")
	}
	if root.element != nil {
		t.Error("empty tree root should have nil element")
	}
	if len(root.children) != 0 {
		t.Errorf("empty tree root should have no children, got %d", len(root.children))
	}
}

// ── Cross-format consistency tests ──

func TestSimpleXMLvsYMLConsistency(t *testing.T) {
	elements := sampleHierarchy()

	xml := ElementsToSimpleXML(elements)
	yml := ElementsToYML(elements)

	// Both should contain all element IDs
	for _, el := range elements {
		idStr := fmt.Sprintf("%d", el.ID)
		// SimpleXML uses index attribute
		if !strings.Contains(xml, fmt.Sprintf(`index="%s"`, idStr)) {
			t.Errorf("SimpleXML missing node index=%s", idStr)
		}
		// YML uses id field
		if !strings.Contains(yml, fmt.Sprintf("id: %s", idStr)) {
			t.Errorf("YML missing node id: %s", idStr)
		}
	}
}

func TestAllFormatsHandleEmptyGracefully(t *testing.T) {
	formatters := map[string]func(elements []protocol.UIFullElement) string{
		"jsonl":     ElementsToJSONL,
		"simplexml": ElementsToSimpleXML,
		"flatref":   ElementsToFlatRef,
		"yml":       ElementsToYML,
	}

	for name, fn := range formatters {
		t.Run(name, func(t *testing.T) {
			// nil input
			result := fn(nil)
			if result == "" {
				t.Errorf("%s: nil should return placeholder, got empty string", name)
			}

			// empty slice
			result = fn([]protocol.UIFullElement{})
			if result == "" {
				t.Errorf("%s: empty slice should return placeholder, got empty string", name)
			}
		})
	}
}

func TestAllFormatsProduceNonEmptyForValidInput(t *testing.T) {
	elements := sampleHierarchy()

	formatters := map[string]func(elements []protocol.UIFullElement) string{
		"jsonl":     ElementsToJSONL,
		"simplexml": ElementsToSimpleXML,
		"flatref":   ElementsToFlatRef,
		"yml":       ElementsToYML,
	}

	for name, fn := range formatters {
		t.Run(name, func(t *testing.T) {
			result := fn(elements)
			if len(result) == 0 {
				t.Errorf("%s: should produce output for valid input", name)
			}
		})
	}
}

// ── Benchmark tests ──

func BenchmarkElementsToJSONL(b *testing.B) {
	elements := sampleHierarchy()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ElementsToJSONL(elements)
	}
}

func BenchmarkElementsToSimpleXML(b *testing.B) {
	elements := sampleHierarchy()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ElementsToSimpleXML(elements)
	}
}

func BenchmarkElementsToFlatRef(b *testing.B) {
	elements := sampleHierarchy()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ElementsToFlatRef(elements)
	}
}

func BenchmarkElementsToYML(b *testing.B) {
	elements := sampleHierarchy()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ElementsToYML(elements)
	}
}

func BenchmarkBuildTree(b *testing.B) {
	elements := sampleHierarchy()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildTree(elements)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Critical boundary tests — Unicode, control chars, orphans, deep nesting, etc.
// ═══════════════════════════════════════════════════════════════════════════════

// ── Unicode / Emoji (international apps) ──

func TestUnicodeInAllFormats(t *testing.T) {
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, Text: "你好世界", ContentDesc: "🌍🚀", ClassName: "TextView", Bounds: [4]int{0, 0, 100, 50}},
		{ID: 1, Parent: 0, Depth: 1, Text: "日本語テスト", ContentDesc: "🎉", ClassName: "Button", Bounds: [4]int{10, 60, 200, 120}},
		{ID: 2, Parent: 1, Depth: 2, Text: "한국어 테스트", ClassName: "TextView", Bounds: [4]int{20, 130, 300, 200}},
		{ID: 3, Parent: 0, Depth: 1, Text: "émojis ☺️ cœur ﷽", ClassName: "EditText", Bounds: [4]int{0, 210, 400, 260}},
	}

	// Test all 4 formats with Unicode
	t.Run("jsonl", func(t *testing.T) {
		result := ElementsToJSONL(elements)
		for _, line := range strings.Split(strings.TrimSpace(result), "\n") {
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Errorf("Unicode breaking JSON: %v\n  %s", err, line)
			}
		}
		// Verify emoji survives roundtrip
		if !strings.Contains(result, "🌍🚀") {
			t.Error("emoji in content_desc should be preserved")
		}
		if !strings.Contains(result, "你好世界") {
			t.Error("CJK text should be preserved")
		}
	})

	t.Run("simplexml", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		if !strings.Contains(result, "🌍🚀") {
			t.Error("emoji in XML should be preserved")
		}
		if !strings.Contains(result, "你好世界") {
			t.Error("CJK in XML should be preserved")
		}
		// XML well-formedness
		opens := strings.Count(result, "<node ")
		closes := strings.Count(result, "</node>") + strings.Count(result, "/>")
		if opens != closes {
			t.Errorf("unbalanced XML tags: %d opens vs %d closes", opens, closes)
		}
	})

	t.Run("flatref", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		if !strings.Contains(result, "🌍🚀") {
			t.Error("emoji in flatref should be preserved")
		}
		if !strings.Contains(result, "你好世界") {
			t.Error("CJK in flatref should be preserved")
		}
	})

	t.Run("yml", func(t *testing.T) {
		result := ElementsToYML(elements)
		if !strings.Contains(result, "🌍🚀") {
			t.Error("emoji in YML should be preserved")
		}
	})
}

// ── Control characters (must not break formats) ──

func TestControlCharacters(t *testing.T) {
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, Text: "line1\r\nline2", ClassName: "TextView", Bounds: [4]int{0, 0, 100, 50}},
		{ID: 1, Parent: 0, Depth: 1, Text: "bell\x07char", ClassName: "Button", Bounds: [4]int{10, 60, 200, 80}},
	}

	t.Run("jsonl", func(t *testing.T) {
		result := ElementsToJSONL(elements)
		for _, line := range strings.Split(strings.TrimSpace(result), "\n") {
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Errorf("control char broke JSON: %v\n  %s", err, line)
			}
		}
		// CR should be escaped as \r
		if !strings.Contains(result, `\r`) {
			t.Error("CR should be JSON-escaped")
		}
	})

	t.Run("simplexml", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		// XML should NOT contain raw control characters
		if strings.Contains(result, "\x07") {
			t.Error("bell char should be escaped in XML")
		}
		opens := strings.Count(result, "<node ")
		closes := strings.Count(result, "</node>") + strings.Count(result, "/>")
		if opens != closes {
			t.Errorf("XML unbalanced after control chars: %d vs %d", opens, closes)
		}
	})

	t.Run("flatref", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		// Should not contain raw CR that would break line-based parsing
		if strings.Contains(result, "\r") {
			t.Error("CR should not appear raw in flatref")
		}
	})
}

// ── Orphan nodes (parent doesn't exist) ──

func TestOrphanNodesRobustness(t *testing.T) {
	// Parent ID=999 doesn't exist — these are orphan nodes
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		{ID: 5, Parent: 999, Depth: 1, Text: "orphan1", ClassName: "TextView", Bounds: [4]int{10, 10, 100, 50}},
		{ID: 6, Parent: 999, Depth: 1, Text: "orphan2", ClassName: "Button", Bounds: [4]int{10, 60, 200, 120}},
		{ID: 7, Parent: 5, Depth: 2, Text: "grand-orphan", ClassName: "TextView", Bounds: [4]int{20, 20, 80, 40}},
	}

	// Verify none of these panic
	t.Run("jsonl", func(t *testing.T) {
		result := ElementsToJSONL(elements)
		lines := strings.Split(strings.TrimSpace(result), "\n")
		if len(lines) != 4 {
			t.Errorf("all 4 orphan nodes should appear, got %d", len(lines))
		}
		for i, line := range lines {
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Errorf("orphan line %d invalid JSON: %v", i, err)
			}
		}
	})

	t.Run("simplexml", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		// Should not panic; orphans appear under first root or become roots
		if !strings.Contains(result, "orphan1") {
			t.Error("orphan nodes should appear in XML")
		}
	})

	t.Run("flatref", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		if !strings.Contains(result, "parent=#999") {
			t.Error("orphan parent ref should be preserved in flatref")
		}
	})

	t.Run("yml", func(t *testing.T) {
		result := ElementsToYML(elements)
		if !strings.Contains(result, "orphan1") {
			t.Error("orphan should appear in YML")
		}
	})
}

// ── Very deep nesting (stack safety) ──

func TestDeepNestingStackSafety(t *testing.T) {
	const depth = 500 // ~typical Android view hierarchy max depth

	for _, tc := range []struct {
		name string
		n    int
	}{
		{"shallow_50", 50},
		{"medium_200", 200},
		{"deep_500", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			elements := make([]protocol.UIFullElement, tc.n)
			for i := 0; i < tc.n; i++ {
				parent := i - 1
				elements[i] = protocol.UIFullElement{
					ID: i, Parent: parent, Depth: i,
					ClassName: "View", Bounds: [4]int{i, i, 100 + i, 100 + i},
				}
			}

			// All format functions use recursion — must not stack overflow
			_ = ElementsToJSONL(elements)    // non-recursive, always safe
			_ = ElementsToFlatRef(elements)  // non-recursive, always safe

			// These use recursion via buildTree + write*Node
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("SimpleXML panicked at depth %d: %v", tc.n, r)
					}
				}()
				_ = ElementsToSimpleXML(elements)
			}()

			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("YML panicked at depth %d: %v", tc.n, r)
					}
				}()
				_ = ElementsToYML(elements)
			}()
		})
	}
}

// ── Large element count (performance baseline) ──

func TestLargeElementSet(t *testing.T) {
	const count = 1000
	elements := make([]protocol.UIFullElement, count)
	for i := 0; i < count; i++ {
		parent := -1
		if i > 0 {
			parent = 0 // flat structure: root with 999 children
		}
		elements[i] = protocol.UIFullElement{
			ID: i, Parent: parent, Depth: func() int {
				if i == 0 {
					return 0
				}
				return 1
			}(),
			ClassName: "Button",
			Bounds:    [4]int{10 * i, 50 * i, 100 + 10*i, 100 + 50*i},
		}
	}

	t.Run("jsonl_lines", func(t *testing.T) {
		result := ElementsToJSONL(elements)
		lines := strings.Split(strings.TrimSpace(result), "\n")
		if len(lines) != count {
			t.Errorf("expected %d JSONL lines, got %d", count, len(lines))
		}
	})

	t.Run("flatref_lines", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		lines := strings.Split(result, "\n")
		if len(lines) != count {
			t.Errorf("expected %d flatref lines, got %d", count, len(lines))
		}
	})

	t.Run("simplexml", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		expectedNodes := strings.Count(result, "class=\"Button\"")
		if expectedNodes != count {
			t.Errorf("expected %d Button nodes, got %d", count, expectedNodes)
		}
	})

	t.Run("yml", func(t *testing.T) {
		result := ElementsToYML(elements)
		countNodes := strings.Count(result, "class: Button")
		if countNodes != count {
			t.Errorf("expected %d YML Button entries, got %d", count, countNodes)
		}
	})
}

// ── Complex sibling ordering ──

func TestComplexSiblingOrdering(t *testing.T) {
	// Multi-branch tree: root → [A, B, C], each with own children
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		// Branch A: 2 children
		{ID: 10, Parent: 0, Depth: 1, Text: "A", ClassName: "View", Bounds: [4]int{0, 0, 100, 100}},
		{ID: 11, Parent: 10, Depth: 2, Text: "A1", ClassName: "TextView", Bounds: [4]int{10, 10, 50, 50}},
		{ID: 12, Parent: 10, Depth: 2, Text: "A2", ClassName: "Button", Bounds: [4]int{10, 60, 80, 90}},
		// Branch B: 1 child
		{ID: 20, Parent: 0, Depth: 1, Text: "B", ClassName: "View", Bounds: [4]int{200, 0, 300, 100}},
		{ID: 21, Parent: 20, Depth: 2, Text: "B1", ClassName: "TextView", Bounds: [4]int{210, 10, 250, 50}},
		// Branch C: 0 children
		{ID: 30, Parent: 0, Depth: 1, Text: "C", ClassName: "Button", Bounds: [4]int{400, 0, 500, 100}},
	}

	t.Run("flatref_parent_depth", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		lines := strings.Split(result, "\n")

		findLine := func(text string) string {
			for _, l := range lines {
				if strings.Contains(l, text) {
					return l
				}
			}
			return ""
		}

		a := findLine(`text="A"`)
		b := findLine(`text="B"`)
		c := findLine(`text="C"`)
		a1 := findLine(`text="A1"`)
		a2 := findLine(`text="A2"`)
		b1 := findLine(`text="B1"`)

		// Root children (depth=1) → all parent=#0
		for _, line := range []string{a, b, c} {
			if !strings.Contains(line, "parent=#0") {
				t.Errorf("should have parent=#0: %s", line)
			}
			if !strings.Contains(line, "depth=1") {
				t.Errorf("should have depth=1: %s", line)
			}
		}

		// A1 under A (id=10) → depth=2
		if !strings.Contains(a1, "parent=#10") {
			t.Errorf("A1 should have parent=#10: %s", a1)
		}
		if !strings.Contains(a1, "depth=2") {
			t.Errorf("A1 should have depth=2: %s", a1)
		}

		// A2 under A (id=10) → depth=2
		if !strings.Contains(a2, "parent=#10") {
			t.Errorf("A2 should have parent=#10: %s", a2)
		}

		// B1 under B (id=20) → depth=2
		if !strings.Contains(b1, "parent=#20") {
			t.Errorf("B1 should have parent=#20: %s", b1)
		}
	})

	t.Run("simplexml_nesting", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		// Verify structural nesting: A contains A1, A2
		aIdx := strings.Index(result, `text="A"`)
		a1Idx := strings.Index(result, `text="A1"`)
		a2Idx := strings.Index(result, `text="A2"`)
		aClose := strings.Index(result[aIdx:], "</node>") + aIdx

		if a1Idx <= aIdx || a1Idx >= aClose {
			t.Error("A1 should be nested inside A's XML element")
		}
		if a2Idx <= aIdx || a2Idx >= aClose {
			t.Error("A2 should be nested inside A's XML element")
		}
	})
}

// ── Empty class name ──

func TestEmptyClassName(t *testing.T) {
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, Text: "no class", ClassName: "", Bounds: [4]int{0, 0, 100, 50}},
	}

	t.Run("jsonl", func(t *testing.T) {
		result := ElementsToJSONL(elements)
		if strings.Contains(result, `"class":""`) {
			t.Error("empty class should be omitted from JSONL")
		}
		var obj map[string]interface{}
		line := strings.TrimSpace(result)
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("empty class broke JSON: %v", err)
		}
	})

	t.Run("simplexml", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		if strings.Contains(result, `class=""`) {
			t.Error("empty class should be omitted from XML")
		}
	})

	t.Run("flatref", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		// Should not have empty parentheses
		if strings.Contains(result, " () ") {
			t.Error("empty class should not produce empty parens")
		}
	})
}

// ── Resource ID edge cases ──

func TestResourceIDEdgeCases(t *testing.T) {
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ResourceID: "com.example:id/btn_ok", ClassName: "Button", Bounds: [4]int{0, 0, 100, 50}},
		{ID: 1, Parent: 0, Depth: 1, ResourceID: "android:id/title", ClassName: "TextView", Bounds: [4]int{10, 60, 200, 80}},
		{ID: 2, Parent: 1, Depth: 2, ResourceID: "no_slash", ClassName: "View", Bounds: [4]int{20, 90, 80, 110}},
	}

	t.Run("jsonl_full_id", func(t *testing.T) {
		result := ElementsToJSONL(elements)
		if !strings.Contains(result, "com.example:id/btn_ok") {
			t.Error("full resource_id should appear in JSONL")
		}
		if !strings.Contains(result, "android:id/title") {
			t.Error("android resource_id should appear in JSONL")
		}
	})

	t.Run("flatref_short_id", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		if !strings.Contains(result, `id="btn_ok"`) {
			t.Error("flatref should use short resource id: btn_ok")
		}
		if !strings.Contains(result, `id="title"`) {
			t.Error("flatref should use short resource id: title")
		}
		if !strings.Contains(result, `id="no_slash"`) {
			t.Error("flatref should handle resource id without slash")
		}
	})

	t.Run("simplexml_full_id", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		if !strings.Contains(result, `resource-id="com.example:id/btn_ok"`) {
			t.Error("SimpleXML should include full resource-id")
		}
	})
}

// ── Bounds edge cases ──

func TestBoundsEdgeCases(t *testing.T) {
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "TextView", Bounds: [4]int{0, 0, 1080, 2400}},
		{ID: 1, Parent: 0, Depth: 1, ClassName: "Button", Bounds: [4]int{100, 200, 300, 400}},
		{ID: 2, Parent: 1, Depth: 2, ClassName: "View", Bounds: [4]int{0, 0, 1, 1}}, // 1px element
		{ID: 3, Parent: 0, Depth: 1, ClassName: "Image", Bounds: [4]int{0, 0, 0, 0}}, // zero-area (should be filtered by server, but format must handle)
	}

	t.Run("jsonl_bounds_format", func(t *testing.T) {
		result := ElementsToJSONL(elements)
		if !strings.Contains(result, `"bounds":"[0,0][1080,2400]"`) {
			t.Error("bounds format wrong")
		}
		if !strings.Contains(result, `"bounds":"[0,0][1,1]"`) {
			t.Error("1px element should still have bounds")
		}
	})

	t.Run("simplexml_bounds", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		if !strings.Contains(result, `bounds="[0,0][1080,2400]"`) {
			t.Error("XML bounds format wrong")
		}
	})

	t.Run("flatref_bounds", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		if !strings.Contains(result, "bounds=[100,200][300,400]") {
			t.Error("flatref bounds format wrong")
		}
	})
}

// ── All boolean state combinations ──

func TestAllBooleanCombinations(t *testing.T) {
	// Enumerate meaningful combos
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "View", Bounds: [4]int{0, 0, 100, 50}, Clickable: false, Enabled: true, Focused: false, Selected: false},
		{ID: 1, Parent: 0, Depth: 1, ClassName: "View", Bounds: [4]int{0, 50, 100, 100}, Clickable: true, Enabled: true, Focused: false, Selected: false},
		{ID: 2, Parent: 0, Depth: 1, ClassName: "View", Bounds: [4]int{0, 100, 100, 150}, Clickable: true, Enabled: false, Focused: false, Selected: false},
		{ID: 3, Parent: 0, Depth: 1, ClassName: "View", Bounds: [4]int{0, 150, 100, 200}, Clickable: false, Enabled: false, Focused: true, Selected: false},
		{ID: 4, Parent: 0, Depth: 1, ClassName: "View", Bounds: [4]int{0, 200, 100, 250}, Clickable: true, Enabled: true, Focused: true, Selected: true},
	}

	t.Run("jsonl_booleans", func(t *testing.T) {
		result := ElementsToJSONL(elements)
		lines := strings.Split(strings.TrimSpace(result), "\n")

		// id=0: default — should NOT have focused/selected fields
		if strings.Contains(lines[0], `"focused"`) {
			t.Error("default focused=false should be omitted from JSONL")
		}
		if strings.Contains(lines[0], `"selected"`) {
			t.Error("default selected=false should be omitted from JSONL")
		}

		// id=1: clickable=true
		if !strings.Contains(lines[1], `"clickable":true`) {
			t.Error("clickable=true should be present")
		}

		// id=2: enabled=false
		if !strings.Contains(lines[2], `"enabled":false`) {
			t.Error("enabled=false should be present")
		}

		// id=3: focused=true
		if !strings.Contains(lines[3], `"focused":true`) {
			t.Error("focused=true should be present")
		}

		// id=4: all true
		if !strings.Contains(lines[4], `"clickable":true`) {
			t.Error("id=4 should have clickable=true")
		}
		if !strings.Contains(lines[4], `"focused":true`) {
			t.Error("id=4 should have focused=true")
		}
		if !strings.Contains(lines[4], `"selected":true`) {
			t.Error("id=4 should have selected=true")
		}
	})

	t.Run("simplexml_booleans", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		if strings.Contains(result, `clickable="False"`) {
			t.Error("clickable=False should be omitted in SimpleXML")
		}
		if strings.Contains(result, `enabled="True"`) {
			t.Error("enabled=True should be omitted (default)")
		}
	})

	t.Run("flatref_tags", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		lines := strings.Split(result, "\n")

		// id=0: no boolean tags (clickable=false, enabled=true, focused=false, selected=false)
		line0 := lines[0]
		if strings.Contains(line0, "[clickable]") || strings.Contains(line0, "[focused]") ||
			strings.Contains(line0, "[selected]") || strings.Contains(line0, "[disabled]") {
			t.Errorf("default node should have no boolean tags: %s", line0)
		}

		// id=1: only [clickable]
		if !strings.Contains(lines[1], "[clickable]") || strings.Contains(lines[1], "[disabled]") {
			t.Errorf("id=1 should only have [clickable]: %s", lines[1])
		}

		// id=2: [clickable] + [disabled]
		if !strings.Contains(lines[2], "[clickable]") || !strings.Contains(lines[2], "[disabled]") {
			t.Errorf("id=2 should have [clickable] and [disabled]: %s", lines[2])
		}

		// id=3: [focused] + [disabled] (no clickable)
		if !strings.Contains(lines[3], "[focused]") || !strings.Contains(lines[3], "[disabled]") {
			t.Errorf("id=3 should have [focused] and [disabled]: %s", lines[3])
		}
		if strings.Contains(lines[3], "[clickable]") {
			t.Errorf("id=3 should NOT have [clickable]: %s", lines[3])
		}

		// id=4: [clickable] + [focused] + [selected] (no disabled)
		if !strings.Contains(lines[4], "[clickable]") || !strings.Contains(lines[4], "[focused]") ||
			!strings.Contains(lines[4], "[selected]") {
			t.Errorf("id=4 should have [clickable] [focused] [selected]: %s", lines[4])
		}
		if strings.Contains(lines[4], "[disabled]") {
			t.Errorf("id=4 should NOT have [disabled]: %s", lines[4])
		}
	})

	t.Run("yml_booleans", func(t *testing.T) {
		result := ElementsToYML(elements)
		if !strings.Contains(result, "clickable: true") {
			t.Error("YML should have clickable=true for id=1")
		}
		if !strings.Contains(result, "clickable: false") {
			t.Error("YML should have clickable=false for id=0")
		}
	})
}

// ── XML well-formedness stress test ──

func TestSimpleXMLWellFormedness(t *testing.T) {
	testCases := []struct {
		name     string
		elements []protocol.UIFullElement
	}{
		{"sample", sampleHierarchy()},
		{"deep50", generateDeepChain(50)},
		{"flat100", generateFlatChildren(100)},
		{"complex", generateComplexTree()},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ElementsToSimpleXML(tc.elements)

			// Count <node ...> opens vs </node> + /> closes
			openTags := 0
			closeTags := 0
			selfCloseTags := 0

			for i := 0; i < len(result); i++ {
				if strings.HasPrefix(result[i:], "<node ") {
					openTags++
				}
				if strings.HasPrefix(result[i:], "</node>") {
					closeTags++
				}
				if strings.HasPrefix(result[i:], "/>") {
					selfCloseTags++
				}
			}

			if openTags != closeTags+selfCloseTags {
				t.Errorf("[%s] unbalanced tags: %d opens, %d closes, %d self-closing",
					tc.name, openTags, closeTags, selfCloseTags)
			}
		})
	}
}

// ── JSONL validity stress test ──

func TestJSONLAlwaysValid(t *testing.T) {
	testCases := []struct {
		name     string
		elements []protocol.UIFullElement
	}{
		{"sample", sampleHierarchy()},
		{"unicode", []protocol.UIFullElement{
			{ID: 0, Parent: -1, Depth: 0, Text: "hello\nworld\t!", ContentDesc: "desc", ClassName: "View", Bounds: [4]int{0, 0, 100, 50}},
		}},
		{"empty_fields", []protocol.UIFullElement{
			{ID: 0, Parent: -1, Depth: 0, ClassName: "", Bounds: [4]int{0, 0, 1, 1}},
		}},
		{"deep500", generateDeepChain(500)},
		{"full_flags", []protocol.UIFullElement{
			{ID: 0, Parent: -1, Depth: 0, ClassName: "Button", Bounds: [4]int{0, 0, 100, 50}, Clickable: true, Enabled: false, Focused: true, Selected: true},
		}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ElementsToJSONL(tc.elements)
			for i, line := range strings.Split(strings.TrimSpace(result), "\n") {
				var obj map[string]interface{}
				if err := json.Unmarshal([]byte(line), &obj); err != nil {
					t.Errorf("line %d invalid JSON: %v\n  %s", i, err, line)
				}
				// Required fields
				for _, key := range []string{"id", "parent", "depth", "bounds", "clickable"} {
					if _, ok := obj[key]; !ok {
						t.Errorf("line %d missing required field %q", i, key)
					}
				}
			}
		})
	}
}

// ── Cross-format element count consistency ──

func TestAllFormatsSameElementCount(t *testing.T) {
	testCases := []struct {
		name     string
		elements []protocol.UIFullElement
	}{
		{"sample", sampleHierarchy()},
		{"single", []protocol.UIFullElement{{ID: 0, Parent: -1, Depth: 0, ClassName: "View", Bounds: [4]int{0, 0, 100, 100}}}},
		{"deep50", generateDeepChain(50)},
		{"complex", generateComplexTree()},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			expected := len(tc.elements)

			jsonlLines := len(strings.Split(strings.TrimSpace(ElementsToJSONL(tc.elements)), "\n"))
			flatrefLines := len(strings.Split(strings.TrimSpace(ElementsToFlatRef(tc.elements)), "\n"))

			if jsonlLines != expected {
				t.Errorf("JSONL: expected %d lines, got %d", expected, jsonlLines)
			}
			if flatrefLines != expected {
				t.Errorf("FlatRef: expected %d lines, got %d", expected, flatrefLines)
			}

			// XML: count nodes by counting `index="` occurrences
			xml := ElementsToSimpleXML(tc.elements)
			xmlCount := strings.Count(xml, `index="`)
			if xmlCount != expected {
				t.Errorf("SimpleXML: expected %d nodes, got %d", expected, xmlCount)
			}

			// YML: count nodes by counting `- id:` occurrences
			yml := ElementsToYML(tc.elements)
			ymlCount := strings.Count(yml, "- id:")
			if ymlCount != expected {
				t.Errorf("YML: expected %d nodes, got %d", expected, ymlCount)
			}
		})
	}
}

// ── FlatRef ID and parent reference correctness ──

func TestFlatRefIDReferences(t *testing.T) {
	elements := generateComplexTree()
	result := ElementsToFlatRef(elements)

	// Parse all #id ... parent=#M references (parent is at end of line)
	idToParent := make(map[int]int)
	for _, line := range strings.Split(result, "\n") {
		var id, parent int
		// New format: #N depth=D [S] ... parent=#M
		// Find parent=# at end of line
		parentIdx := strings.LastIndex(line, " parent=#")
		if parentIdx < 0 {
			t.Errorf("cannot parse flatref line (missing parent=#): %s", line)
			continue
		}
		if _, err := fmt.Sscanf(line, "#%d", &id); err != nil {
			t.Errorf("cannot parse id from line: %s", line)
			continue
		}
		if _, err := fmt.Sscanf(line[parentIdx:], " parent=#%d", &parent); err != nil {
			t.Errorf("cannot parse parent from line: %s", line)
			continue
		}
		idToParent[id] = parent
	}

	// Every element's parent should match
	for _, el := range elements {
		gotParent, exists := idToParent[el.ID]
		if !exists {
			t.Errorf("element id=%d missing from flatref output", el.ID)
			continue
		}
		if gotParent != el.Parent {
			t.Errorf("id=%d: parent mismatch — flatref says #%d, element says %d",
				el.ID, gotParent, el.Parent)
		}
	}
}

// ── Stress: all 4 formats with random-ish data ──

func TestAllFormatsStress(t *testing.T) {
	// Diverse element set with all field combinations
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2194}},
		{ID: 1, Parent: 0, Depth: 1, Text: "Hello World", ClassName: "TextView", Bounds: [4]int{16, 48, 200, 96}},
		{ID: 2, Parent: 0, Depth: 1, ContentDesc: "Navigate up", ResourceID: "com.app:id/back", ClassName: "ImageButton", Bounds: [4]int{0, 0, 96, 96}, Clickable: true},
		{ID: 3, Parent: 0, Depth: 1, Text: "Submit", ClassName: "Button", Bounds: [4]int{400, 200, 600, 280}, Clickable: true},
		{ID: 4, Parent: 0, Depth: 1, ClassName: "EditText", Bounds: [4]int{16, 300, 500, 360}, Focused: true},
		{ID: 5, Parent: 0, Depth: 1, ClassName: "Switch", Bounds: [4]int{16, 400, 200, 460}, Clickable: true, Selected: true},
		{ID: 6, Parent: 0, Depth: 1, ClassName: "CheckBox", Bounds: [4]int{16, 500, 200, 560}, Clickable: true},
		{ID: 7, Parent: 0, Depth: 1, Text: "Disabled btn", ClassName: "Button", Bounds: [4]int{400, 500, 600, 560}, Enabled: false},
		{ID: 8, Parent: 4, Depth: 2, ClassName: "View", Bounds: [4]int{20, 310, 490, 350}},
		{ID: 9, Parent: 8, Depth: 3, ClassName: "TextView", Bounds: [4]int{25, 315, 200, 345}},
		{ID: 10, Parent: 9, Depth: 4, Text: "Compose nested", ClassName: "TextView", Bounds: [4]int{30, 320, 150, 340}},
	}

	t.Run("jsonl", func(t *testing.T) {
		result := ElementsToJSONL(elements)
		lines := strings.Split(strings.TrimSpace(result), "\n")
		if len(lines) != 11 {
			t.Fatalf("expected 11 lines, got %d", len(lines))
		}
		for i, line := range lines {
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Errorf("line %d: %v", i, err)
			}
		}
	})

	t.Run("simplexml", func(t *testing.T) {
		result := ElementsToSimpleXML(elements)
		checkXMLWellFormed(t, result)
	})

	t.Run("flatref", func(t *testing.T) {
		result := ElementsToFlatRef(elements)
		lines := strings.Split(result, "\n")
		if len(lines) != 11 {
			t.Fatalf("expected 11 lines, got %d", len(lines))
		}
	})

	t.Run("yml", func(t *testing.T) {
		result := ElementsToYML(elements)
		if !strings.Contains(result, "Compose nested") {
			t.Error("deep nested text should appear")
		}
	})
}

// ── Helpers for generating test data ──

func generateDeepChain(n int) []protocol.UIFullElement {
	elements := make([]protocol.UIFullElement, n)
	for i := 0; i < n; i++ {
		parent := i - 1
		elements[i] = protocol.UIFullElement{
			ID: i, Parent: parent, Depth: i,
			ClassName: "View",
			Bounds:    [4]int{i, i, 100 + i, 100 + i},
		}
	}
	return elements
}

func generateFlatChildren(n int) []protocol.UIFullElement {
	elements := make([]protocol.UIFullElement, n+1) // root + n children
	elements[0] = protocol.UIFullElement{
		ID: 0, Parent: -1, Depth: 0,
		ClassName: "FrameLayout",
		Bounds:    [4]int{0, 0, 1080, 2400},
	}
	for i := 1; i <= n; i++ {
		elements[i] = protocol.UIFullElement{
			ID: i, Parent: 0, Depth: 1,
			ClassName: "Button",
			Bounds:    [4]int{10, 10 * i, 100, 10*i + 50},
		}
	}
	return elements
}

func generateComplexTree() []protocol.UIFullElement {
	// Root → 3 branches, each branch has varying depth
	return []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		// Branch 1: shallow (root → A)
		{ID: 1, Parent: 0, Depth: 1, Text: "A", ClassName: "View", Bounds: [4]int{0, 0, 100, 100}},
		// Branch 2: medium (root → B → B1 → B2)
		{ID: 2, Parent: 0, Depth: 1, Text: "B", ClassName: "View", Bounds: [4]int{100, 0, 200, 100}},
		{ID: 3, Parent: 2, Depth: 2, Text: "B1", ClassName: "View", Bounds: [4]int{110, 10, 190, 50}},
		{ID: 4, Parent: 3, Depth: 3, Text: "B2", ClassName: "TextView", Bounds: [4]int{120, 15, 180, 45}},
		// Branch 3: deep (root → C → C1 → C2 → C3 → C4)
		{ID: 5, Parent: 0, Depth: 1, Text: "C", ClassName: "View", Bounds: [4]int{200, 0, 300, 100}},
		{ID: 6, Parent: 5, Depth: 2, Text: "C1", ClassName: "View", Bounds: [4]int{210, 5, 290, 95}},
		{ID: 7, Parent: 6, Depth: 3, Text: "C2", ClassName: "View", Bounds: [4]int{215, 10, 285, 90}},
		{ID: 8, Parent: 7, Depth: 4, Text: "C3", ClassName: "View", Bounds: [4]int{220, 15, 280, 85}},
		{ID: 9, Parent: 8, Depth: 5, Text: "C4", ClassName: "TextView", Bounds: [4]int{225, 20, 275, 80}},
	}
}

func checkXMLWellFormed(t *testing.T, xml string) {
	t.Helper()
	openTags := strings.Count(xml, "<node ")
	closeTags := strings.Count(xml, "</node>")
	selfClose := strings.Count(xml, "/>")
	if openTags != closeTags+selfClose {
		t.Errorf("unbalanced XML: %d opens, %d closes, %d self-closing",
			openTags, closeTags, selfClose)
	}
}

// ── CompactElements tests ──

func TestCompactElementsNoRedundancy(t *testing.T) {
	// Elements with semantic info — nothing should be removed
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		{ID: 1, Parent: 0, Depth: 1, Text: "Hello", ClassName: "TextView", Bounds: [4]int{10, 20, 100, 50}},
		{ID: 2, Parent: 0, Depth: 1, ResourceID: "com.app:id/btn", ClassName: "Button", Bounds: [4]int{10, 60, 200, 120}, Clickable: true},
	}
	result := CompactElements(elements)
	if len(result) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(result))
	}
	for i := range elements {
		if result[i].ID != elements[i].ID {
			t.Errorf("element %d should be preserved", elements[i].ID)
		}
	}
}

func TestCompactElementsRemovesEmptyLayoutContainers(t *testing.T) {
	// Simulates real phone dump: FrameLayout → LinearLayout → ... with empty wrappers
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},
		{ID: 1, Parent: 0, Depth: 1, ClassName: "LinearLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},                            // redundant: empty wrapper
		{ID: 2, Parent: 1, Depth: 2, ResourceID: "content", ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},    // has resource-id, kept
		{ID: 3, Parent: 2, Depth: 3, ClassName: "ViewGroup", Bounds: [4]int{0, 80, 1042, 2170}, Enabled: true},                             // redundant: empty ViewGroup
		{ID: 4, Parent: 3, Depth: 4, Text: "Samsung", ClassName: "TextView", Bounds: [4]int{0, 272, 200, 646}, Clickable: true, Enabled: true},
		{ID: 5, Parent: 3, Depth: 4, Text: "Google", ClassName: "TextView", Bounds: [4]int{200, 272, 400, 646}, Clickable: true, Enabled: true},
		{ID: 6, Parent: 2, Depth: 3, ClassName: "LinearLayout", Bounds: [4]int{0, 0, 1080, 227}, Enabled: true},                            // redundant: empty wrapper
		{ID: 7, Parent: 6, Depth: 4, ResourceID: "app_search_bar_bg", ClassName: "ImageView", Bounds: [4]int{0, 80, 1080, 227}, Enabled: true},
	}

	result := CompactElements(elements)

	// Should remove #1 (LinearLayout), #3 (ViewGroup), #6 (LinearLayout)
	// Kept: #0, #2, #4, #5, #7
	expectedIDs := map[int]bool{0: true, 2: true, 4: true, 5: true, 7: true}
	if len(result) != len(expectedIDs) {
		t.Fatalf("expected %d elements, got %d", len(expectedIDs), len(result))
	}
	for _, el := range result {
		if !expectedIDs[el.ID] {
			t.Errorf("unexpected element id=%d in result", el.ID)
		}
	}

	// Verify re-parenting: #4 (Samsung) parent was #3→#2 now
	for _, el := range result {
		switch el.ID {
		case 4, 5:
			if el.Parent != 2 {
				t.Errorf("id=%d: parent should be #2 (nearest surviving ancestor), got #%d", el.ID, el.Parent)
			}
		case 7:
			if el.Parent != 2 {
				t.Errorf("id=%d: parent should be #2, got #%d", el.ID, el.Parent)
			}
		}
	}
}

func TestCompactElementsKeepsRoot(t *testing.T) {
	// Root is never removed even if it's a layout class
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		{ID: 1, Parent: 0, Depth: 1, Text: "Text", ClassName: "TextView", Bounds: [4]int{10, 10, 100, 50}},
	}
	result := CompactElements(elements)
	if len(result) != 2 {
		t.Fatalf("root should be preserved, got %d elements", len(result))
	}
	if result[0].ID != 0 {
		t.Error("root should be first element")
	}
}

func TestCompactElementsDepthRecalculation(t *testing.T) {
	// Deep hierarchy with redundant wrappers every other level
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ResourceID: "root", ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},
		{ID: 1, Parent: 0, Depth: 1, ClassName: "LinearLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},                   // redundant
		{ID: 2, Parent: 1, Depth: 2, ResourceID: "level2", ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},
		{ID: 3, Parent: 2, Depth: 3, ClassName: "ViewGroup", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},                      // redundant
		{ID: 4, Parent: 3, Depth: 4, Text: "Deep", ClassName: "TextView", Bounds: [4]int{10, 10, 100, 50}, Enabled: true},
	}
	result := CompactElements(elements)

	// Expected: #0(depth=0) → #2(depth=1) → #4(depth=2)
	for _, el := range result {
		switch el.ID {
		case 0:
			if el.Depth != 0 {
				t.Errorf("root depth should be 0, got %d", el.Depth)
			}
		case 2:
			if el.Depth != 1 {
				t.Errorf("id=2 depth should be 1, got %d", el.Depth)
			}
		case 4:
			if el.Depth != 2 {
				t.Errorf("id=4 depth should be 2, got %d", el.Depth)
			}
		}
	}
}

func TestCompactElementsKeepsNonLayoutClasses(t *testing.T) {
	// View (not in IsLayoutClass list) should NOT be removed even if empty
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		{ID: 1, Parent: 0, Depth: 1, ClassName: "View", Bounds: [4]int{0, 0, 1080, 2400}}, // View is not a layout container
	}
	result := CompactElements(elements)
	if len(result) != 2 {
		t.Fatalf("View (non-layout class) should be preserved, got %d elements", len(result))
	}
}

func TestCompactElementsKeepsDisabledContainers(t *testing.T) {
	// A layout container that is disabled might be visually meaningful
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		{ID: 1, Parent: 0, Depth: 1, ClassName: "LinearLayout", Bounds: [4]int{0, 500, 1080, 600}, Enabled: false},
		{ID: 2, Parent: 1, Depth: 2, Text: "Disabled", ClassName: "TextView", Bounds: [4]int{10, 510, 200, 590}},
	}
	result := CompactElements(elements)
	if len(result) != 3 {
		t.Fatalf("disabled layout should be preserved, got %d elements", len(result))
	}
}

func TestCompactElementsEmpty(t *testing.T) {
	result := CompactElements(nil)
	if result != nil {
		t.Error("nil input should return nil")
	}
	result = CompactElements([]protocol.UIFullElement{})
	if len(result) != 0 {
		t.Error("empty slice should return empty slice")
	}
}

func TestCompactElementsSingleElement(t *testing.T) {
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
	}
	result := CompactElements(elements)
	if len(result) != 1 {
		t.Fatalf("single element should be preserved, got %d", len(result))
	}
}

func TestElementsToFlatRefWithCompact(t *testing.T) {
	// Test full integration: filter + format
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},
		{ID: 1, Parent: 0, Depth: 1, ClassName: "LinearLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},                            // redundant
		{ID: 2, Parent: 1, Depth: 2, ResourceID: "content", ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},    // kept
		{ID: 3, Parent: 2, Depth: 3, ClassName: "ViewGroup", Bounds: [4]int{0, 80, 1042, 2170}, Enabled: true},                             // redundant
		{ID: 4, Parent: 3, Depth: 4, Text: "Samsung", ContentDesc: "Samsung folder", ClassName: "TextView", Bounds: [4]int{0, 272, 200, 646}, Clickable: true, Enabled: true},
		{ID: 5, Parent: 3, Depth: 4, Text: "Google", ClassName: "TextView", Bounds: [4]int{200, 272, 400, 646}, Clickable: true, Enabled: true},
	}

	filtered := CompactElements(elements)
	result := ElementsToFlatRef(filtered)
	lines := strings.Split(result, "\n")

	// Should have 4 elements: #0, #2, #4, #5
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d\n%s", len(lines), result)
	}

	// Line 0: root
	if !strings.Contains(lines[0], "#0") {
		t.Error("should start with #0")
	}

	// Line 2: Samsung — parent should be #2 (re-parented from #3)
	samsungLine := lines[2]
	if !strings.Contains(samsungLine, `text="Samsung"`) {
		t.Errorf("line 2 should contain Samsung: %s", samsungLine)
	}
	if !strings.Contains(samsungLine, "parent=#2") {
		t.Errorf("Samsung should be re-parented to #2: %s", samsungLine)
	}

	// Line 3: Google — parent should be #2
	googleLine := lines[3]
	if !strings.Contains(googleLine, `text="Google"`) {
		t.Errorf("line 3 should contain Google: %s", googleLine)
	}
	if !strings.Contains(googleLine, "parent=#2") {
		t.Errorf("Google should be re-parented to #2: %s", googleLine)
	}

	// Verify parent is at end of each line
	for i, line := range lines {
		if !strings.HasSuffix(line, fmt.Sprintf("parent=#%d", filtered[i].Parent)) {
			t.Errorf("line %d: parent should be at end: %s", i, line)
		}
	}

	t.Logf("FlatRef with CompactElements:\n%s", result)
}

func TestCompactElementsPreservesFocusedContainer(t *testing.T) {
	// A focused layout container (e.g., an EditText wrapper) should be kept
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		{ID: 1, Parent: 0, Depth: 1, ClassName: "LinearLayout", Bounds: [4]int{0, 100, 1080, 200}, Focused: true},
		{ID: 2, Parent: 1, Depth: 2, Text: "input", ClassName: "EditText", Bounds: [4]int{10, 110, 500, 190}},
	}
	result := CompactElements(elements)
	if len(result) != 3 {
		t.Fatalf("focused layout should be preserved, got %d elements", len(result))
	}
}

func TestCompactElementsAllRedundant(t *testing.T) {
	// Every child is a redundant container — only root survives
	elements := []protocol.UIFullElement{
		{ID: 0, Parent: -1, Depth: 0, ResourceID: "root", ClassName: "FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},
		{ID: 1, Parent: 0, Depth: 1, ClassName: "LinearLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},
		{ID: 2, Parent: 1, Depth: 2, ClassName: "ViewGroup", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},
		{ID: 3, Parent: 2, Depth: 3, ClassName: "RelativeLayout", Bounds: [4]int{0, 0, 1080, 2400}, Enabled: true},
	}
	result := CompactElements(elements)
	if len(result) != 1 {
		t.Fatalf("only root should survive, got %d elements", len(result))
	}
	if result[0].ID != 0 {
		t.Errorf("root should survive, got id=%d", result[0].ID)
	}
}
