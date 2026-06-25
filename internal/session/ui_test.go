package session

import (
	"testing"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

func TestParseNodeLine(t *testing.T) {
	// Simulated uiautomator XML node line
	tests := []struct {
		name          string
		line          string
		wantText      string
		wantClickable bool
	}{
		{
			name:          "clickable button",
			line:          `<node index="0" text="Settings" resource-id="com.android.settings:id/dashboard_tile" class="android.widget.TextView" package="com.android.settings" content-desc="" checkable="false" checked="false" clickable="true" enabled="true" focusable="false" focused="false" scrollable="false" long-clickable="false" password="false" selected="false" bounds="[0,100][1080,250]" />`,
			wantText:      "Settings",
			wantClickable: true,
		},
		{
			name:          "non-clickable text",
			line:          `<node index="1" text="Hello" resource-id="" class="android.widget.TextView" package="com.example" content-desc="" clickable="false" enabled="true" bounds="[16,48][200,96]" />`,
			wantText:      "Hello",
			wantClickable: false,
		},
		{
			name:          "element with content-desc",
			line:          `<node index="2" text="" resource-id="com.example:id/back_btn" class="android.widget.ImageButton" package="com.example" content-desc="Navigate up" clickable="true" enabled="true" bounds="[0,0][96,96]" />`,
			wantText:      "",
			wantClickable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			el := parseNodeLine(tt.line)
			if el == nil {
				t.Fatal("expected non-nil element")
			}
			if el.Text != tt.wantText {
				t.Errorf("text = %q, want %q", el.Text, tt.wantText)
			}
			if el.Clickable != tt.wantClickable {
				t.Errorf("clickable = %v, want %v", el.Clickable, tt.wantClickable)
			}
		})
	}
}

func TestParseNodeLineInvalid(t *testing.T) {
	// Empty bounds should return nil
	el := parseNodeLine(`<node text="Test" bounds="[0,0][0,0]" />`)
	if el != nil {
		t.Error("expected nil for zero-bounds element")
	}
}

func TestParseUIXML(t *testing.T) {
	xml := `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" content-desc="" clickable="false" bounds="[0,0][1080,2400]">
    <node index="1" text="Settings" resource-id="com.test:id/title" class="android.widget.TextView" content-desc="" clickable="true" bounds="[100,200][500,300]" />
    <node index="2" text="Back" resource-id="" class="android.widget.Button" content-desc="Go back" clickable="true" bounds="[0,0][96,96]" />
  </node>
</hierarchy>`

	elements, err := parseUIXML(xml)
	if err != nil {
		t.Fatal(err)
	}

	if len(elements) < 2 {
		t.Fatalf("expected at least 2 elements, got %d", len(elements))
	}

	// Check the Settings text element
	found := false
	for _, el := range elements {
		if el.Text == "Settings" && el.Clickable {
			found = true
			break
		}
	}
	if !found {
		t.Error("clickable 'Settings' element not found")
	}
}

func TestExtractAttr(t *testing.T) {
	tests := []struct {
		s    string
		name string
		want string
	}{
		{`text="Hello"`, "text", "Hello"},
		{`text=""`, "text", ""},
		{`bounds="[0,100][1080,250]"`, "bounds", "[0,100][1080,250]"},
		{`clickable="true"`, "clickable", "true"},
		{`no-match`, "text", ""},
	}

	for _, tt := range tests {
		got := extractAttr(tt.s, tt.name)
		if got != tt.want {
			t.Errorf("extractAttr(%q, %q) = %q, want %q", tt.s, tt.name, got, tt.want)
		}
	}
}

// Ensure protocol import compiles
var _ = protocol.UIDumpRequest
