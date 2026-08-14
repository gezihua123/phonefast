package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// UISummaryRequest is the summary-mode request prefix.
// Summary mode filters out layout containers on the server side.
const UISummaryRequest = "sum"

// UIFullRequest is the full hierarchical-mode request prefix.
// Full mode returns ALL nodes (no filtering) with parent/depth metadata
// for generating hierarchical formats (jsonl, simplexml, flatref).
const UIFullRequest = "full"

// DefMaxElements is the server-side default max elements per dump.
// Kept in sync with UISocketHandler.ABSOLUTE_MAX_ELEMENTS.
const DefMaxElements = 2000

// DefClientMaxElements is the client-facing default for the max_elements
// parameter across ALL surfaces (CLI, MCP tools, raw RPC). The daemon
// applies it when a caller omits the param; the CLI/MCP layers pass it as
// their default. One constant so the three surfaces can't drift.
const DefClientMaxElements = 100

// UIElement represents a single UI element on screen.
// Compatible with phone-mcp UIElement format.
type UIElement struct {
	Index       int    `json:"index"`
	Text        string `json:"text"`
	ContentDesc string `json:"content_desc"`
	ResourceID  string `json:"resource_id"`
	ClassName   string `json:"class_name"`
	Bounds      [4]int `json:"bounds"` // [left, top, right, bottom]
	Center      [2]int `json:"center"`
	Clickable   bool   `json:"clickable"`
	Enabled     bool   `json:"enabled"`
	Focused     bool   `json:"focused,omitempty"`
	Selected    bool   `json:"selected,omitempty"`
	Visible     *bool  `json:"visible,omitempty"` // nil or true = visible, false = off-screen
}

// IsVisible returns true if the element is visible (default when field is absent).
func (e UIElement) IsVisible() bool {
	return e.Visible == nil || *e.Visible
}

// UIDumpResponse is the parsed response from the ui socket.
type UIDumpResponse struct {
	Elements []UIElement `json:"elements"`
	Error    string      `json:"error,omitempty"`
}

// UIFullElement represents a single UI element with hierarchy metadata.
// Includes parent ID and depth for building tree-based formats.
type UIFullElement struct {
	ID          int    `json:"id"`
	Parent      int    `json:"parent"`
	Depth       int    `json:"depth"`
	Text        string `json:"text"`
	ContentDesc string `json:"content_desc"`
	ResourceID  string `json:"resource_id"`
	ClassName   string `json:"class_name"`
	Bounds      [4]int `json:"bounds"` // [left, top, right, bottom]
	Center      [2]int `json:"center"`
	Clickable   bool   `json:"clickable"`
	Enabled     bool   `json:"enabled"`
	Focused     bool   `json:"focused,omitempty"`
	Selected    bool   `json:"selected,omitempty"`
	Visible     *bool  `json:"visible,omitempty"` // nil or true = visible, false = off-screen
}

// IsVisible returns true if the element is visible (default when field is absent).
func (e UIFullElement) IsVisible() bool {
	return e.Visible == nil || *e.Visible
}

// UIFullResponse is the parsed response from the ui socket in full hierarchical mode.
type UIFullResponse struct {
	Elements []UIFullElement `json:"elements"`
	Error    string          `json:"error,omitempty"`
}

// ReadUIDumpResponse reads a UI dump response from the ui socket.
// Protocol: 4-byte big-endian length prefix + JSON payload.
func ReadUIDumpResponse(r io.Reader) (*UIDumpResponse, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("read ui response length: %w", err)
	}

	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 || length > 10*1024*1024 { // 10MB sanity cap
		return nil, fmt.Errorf("invalid ui response length: %d", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read ui response body: %w", err)
	}

	var resp UIDumpResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal ui response: %w", err)
	}

	return &resp, nil
}

// IsLayoutClass checks if a class name (fully qualified or simple) represents
// a known Android layout container that should be filtered in summary mode.
func IsLayoutClass(className string) bool {
	if className == "" {
		return false
	}
	// Extract simple name after last '.'
	simple := className
	if idx := len(className) - 1; idx >= 0 {
		if dot := strings.LastIndexByte(className, '.'); dot >= 0 {
			simple = className[dot+1:]
		}
	}

	switch simple {
	case "FrameLayout", "LinearLayout", "RelativeLayout", "ConstraintLayout",
		"AbsoluteLayout", "GridLayout", "TableLayout", "TableRow",
		"ScrollView", "HorizontalScrollView", "NestedScrollView",
		"ViewGroup", "ViewStub", "Space", "Spacer",
		"CoordinatorLayout", "DrawerLayout", "SwipeRefreshLayout",
		"Toolbar", "ToolbarLayout", "ActionBar", "ActionBarContainer",
		"BottomNavigationView", "TabLayout", "TabWidget",
		"ViewPager", "ViewPager2", "ViewAnimator", "ViewFlipper",
		"FragmentBreadCrumbs", "ContentFrameLayout":
		return true
	}
	return false
}

// SimplifyClassName shortens Android widget class names for summary mode display.
// e.g. "android.widget.TextView" → "Text", "android.widget.ImageView" → "Image".
// Handles both fully-qualified and simple (already-stripped) names.
func SimplifyClassName(className string) string {
	if className == "" {
		return ""
	}
	// Extract simple name after last '.'
	simple := className
	if idx := strings.LastIndexByte(className, '.'); idx >= 0 {
		simple = className[idx+1:]
	}

	switch simple {
	case "TextView", "CheckedTextView",
		"AppCompatTextView", "MaterialTextView":
		return "Text"
	case "ImageView",
		"AppCompatImageView", "MaterialImageView":
		return "Image"
	case "Button",
		"AppCompatButton", "MaterialButton":
		return "Button"
	case "ImageButton":
		return "IconBtn"
	case "EditText",
		"AppCompatEditText", "MaterialEditText":
		return "Input"
	case "CheckBox",
		"AppCompatCheckBox", "MaterialCheckBox":
		return "Check"
	case "RadioButton",
		"AppCompatRadioButton", "MaterialRadioButton":
		return "Radio"
	case "Switch", "SwitchCompat", "MaterialSwitch":
		return "Switch"
	case "ProgressBar",
		"AppCompatProgressBar", "MaterialProgressBar":
		return "Progress"
	case "SeekBar",
		"AppCompatSeekBar", "MaterialSeekBar":
		return "Seek"
	case "RatingBar":
		return "Rating"
	case "Spinner":
		return "Select"
	case "ToggleButton":
		return "Toggle"
	case "WebView":
		return "Browser"
	}
	return simple
}

// WriteUISummaryRequest sends a summary-mode dump request on the ui socket.
// Summary mode filters out layout containers on the server side.
// If maxElements > 0, includes a limit: "sum:NNN\0".
// Otherwise sends "sum\0" (server uses its default, DefMaxElements).
func WriteUISummaryRequest(w io.Writer, maxElements int) error {
	var req string
	if maxElements > 0 {
		req = fmt.Sprintf("%s:%d\x00", UISummaryRequest, maxElements)
	} else {
		req = fmt.Sprintf("%s\x00", UISummaryRequest)
	}
	_, err := w.Write([]byte(req))
	return err
}

// WriteUIFullRequest sends a full hierarchical-mode dump request on the ui socket.
// Full mode returns ALL nodes with parent/depth metadata.
// If maxElements > 0, includes a limit: "full:NNN\0".
// Otherwise sends "full\0" (server uses its default, DefMaxElements).
func WriteUIFullRequest(w io.Writer, maxElements int) error {
	var req string
	if maxElements > 0 {
		req = fmt.Sprintf("%s:%d\x00", UIFullRequest, maxElements)
	} else {
		req = fmt.Sprintf("%s\x00", UIFullRequest)
	}
	_, err := w.Write([]byte(req))
	return err
}

// ReadUIFullResponse reads a full hierarchical UI dump response from the ui socket.
// Protocol: 4-byte big-endian length prefix + JSON payload.
func ReadUIFullResponse(r io.Reader) (*UIFullResponse, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("read ui full response length: %w", err)
	}

	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 || length > 10*1024*1024 { // 10MB sanity cap
		return nil, fmt.Errorf("invalid ui full response length: %d", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read ui full response body: %w", err)
	}

	var resp UIFullResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal ui full response: %w", err)
	}

	return &resp, nil
}
