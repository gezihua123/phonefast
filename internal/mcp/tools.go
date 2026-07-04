package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gezihua123/phonefast/internal/adb"
	"github.com/gezihua123/phonefast/internal/session"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// ── Tool registration ──

func (s *Server) registerTools() {
	// list_devices
	s.mcpServer.AddTool(
		mcp.NewTool("list_devices",
			mcp.WithDescription("List all connected Android devices."),
		),
		s.handleListDevices,
	)

	// screenshot
	s.mcpServer.AddTool(
		mcp.NewTool("screenshot",
			mcp.WithDescription("Capture the current device screen as base64 PNG."),
		),
		s.handleScreenshot,
	)

	// get_ui_elements
	s.mcpServer.AddTool(
		mcp.NewTool("get_ui_elements",
			mcp.WithDescription("Get interactive UI elements from the current screen."),
				mcp.WithNumber("max_elements",
					mcp.Description("Max number of elements to show (default: 100, -1 for all)."),
				),
		),
		s.handleGetUIElements,
	)

	// tap
	s.mcpServer.AddTool(
		mcp.NewTool("tap",
			mcp.WithDescription("Tap at specified coordinates."),
			mcp.WithNumber("x",
				mcp.Required(),
				mcp.Description("X coordinate"),
			),
			mcp.WithNumber("y",
				mcp.Required(),
				mcp.Description("Y coordinate"),
			),
		),
		s.handleTap,
	)

	// tap_element
	s.mcpServer.AddTool(
		mcp.NewTool("tap_element",
			mcp.WithDescription("Tap a UI element by index or text."),
			mcp.WithNumber("index",
				mcp.Description("Element index to tap"),
			),
			mcp.WithString("text",
				mcp.Description("Element text to search for and tap"),
			),
		),
		s.handleTapElement,
	)

	// swipe
	s.mcpServer.AddTool(
		mcp.NewTool("swipe",
			mcp.WithDescription("Perform a swipe gesture."),
			mcp.WithNumber("start_x",
				mcp.Required(),
				mcp.Description("Start X coordinate"),
			),
			mcp.WithNumber("start_y",
				mcp.Required(),
				mcp.Description("Start Y coordinate"),
			),
			mcp.WithNumber("end_x",
				mcp.Required(),
				mcp.Description("End X coordinate"),
			),
			mcp.WithNumber("end_y",
				mcp.Required(),
				mcp.Description("End Y coordinate"),
			),
			mcp.WithNumber("duration_ms",
				mcp.Description("Swipe duration in milliseconds (default: 500)"),
			),
		),
		s.handleSwipe,
	)

	// type_text
	s.mcpServer.AddTool(
		mcp.NewTool("type_text",
			mcp.WithDescription("Type text into the current field."),
			mcp.WithString("text",
				mcp.Required(),
				mcp.Description("Text to type"),
			),
		),
		s.handleTypeText,
	)

	// back
	s.mcpServer.AddTool(
		mcp.NewTool("back",
			mcp.WithDescription("Press the back button."),
		),
		s.handleBack,
	)

	// home
	s.mcpServer.AddTool(
		mcp.NewTool("home",
			mcp.WithDescription("Press the home button."),
		),
		s.handleHome,
	)

	// press_key
	s.mcpServer.AddTool(
		mcp.NewTool("press_key",
			mcp.WithDescription("Send a key event."),
			mcp.WithNumber("keycode",
				mcp.Description("Android keycode (e.g., 4 for back, 3 for home)"),
			),
			mcp.WithString("key",
				mcp.Description("Key name (e.g., BACK, HOME, ENTER)"),
			),
		),
		s.handlePressKey,
	)

	// launch_app
	s.mcpServer.AddTool(
		mcp.NewTool("launch_app",
			mcp.WithDescription("Launch an app by Android package name (e.g. com.android.settings). Does not resolve display names."),
			mcp.WithString("app",
				mcp.Description("App name to launch"),
			),
			mcp.WithString("package",
				mcp.Description("App package name to launch"),
			),
		),
		s.handleLaunchApp,
	)

	// wait
	s.mcpServer.AddTool(
		mcp.NewTool("wait",
			mcp.WithDescription("Wait for a specified duration in milliseconds."),
			mcp.WithNumber("duration_ms",
				mcp.Description("Duration to wait in milliseconds (default: 1000)"),
			),
		),
		s.handleWait,
	)

	// observe
	s.mcpServer.AddTool(
		mcp.NewTool("observe",
			mcp.WithDescription("Capture screenshot and UI elements in one call (fast)."),
			mcp.WithNumber("max_elements",
				mcp.Description("Max number of elements to show (default: 100, -1 for all)."),
			),
			mcp.WithBoolean("summary",
				mcp.Description("If true, filter out layout containers, return only meaningful elements (default: false)."),
			),
		),
		s.handleObserve,
	)
}

// ── Tool handlers ──

func (s *Server) handleListDevices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	devices, err := adb.ListDevices()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	type deviceInfo struct {
		Serial string `json:"serial"`
		Model  string `json:"model,omitempty"`
		Status string `json:"status"`
	}

	var list []deviceInfo
	for _, d := range devices {
		list = append(list, deviceInfo{
			Serial: d.Serial,
			Model:  d.Model,
			Status: d.Status,
		})
	}

	data, _ := json.Marshal(list)
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleScreenshot(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	pngData, w, h, err := sess.Screenshot()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Return native MCP ImageContent so multimodal LLMs decode the PNG
	// directly. A short text caption carries the dimensions.
	b64 := base64.StdEncoding.EncodeToString(pngData)
	return mcp.NewToolResultImage(
		fmt.Sprintf("Screenshot (%dx%d)", w, h),
		b64,
		"image/png",
	), nil
}

func (s *Server) handleGetUIElements(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	maxShow := getMaxElements(req, 100)
	collectMax := maxShow
	if collectMax < 0 || collectMax > 500 {
		collectMax = 0 // server default (500 for full, 100 for summary)
	}
	isSummary := getSummary(req)
	var elements []protocol.UIElement
	if isSummary {
		elements, err = sess.GetUISummary(collectMax)
	} else {
		elements, err = sess.GetUIElements(collectMax)
	}
	if err != nil {
		elements, err = sess.GetUIElementsFallbackADB(collectMax)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}

	formatted := formatElementsForLLM(elements, maxShow, isSummary)
	return mcp.NewToolResultText(formatted), nil
}

func (s *Server) handleTap(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	xVal, xOk := args["x"].(float64)
	yVal, yOk := args["y"].(float64)
	if !xOk || !yOk {
		return mcp.NewToolResultError("missing required parameters: x and y"), nil
	}

	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := sess.Tap(int(xVal), int(yVal)); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Tapped at (%d, %d)", int(xVal), int(yVal))), nil
}

func (s *Server) handleTapElement(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	elements, fastErr := sess.GetUIElements(0) // collect all elements (server default 500)
	if fastErr != nil {
		var fallbackErr error
		elements, fallbackErr = sess.GetUIElementsFallbackADB(0)
		if fallbackErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"ui dump failed: %v; adb fallback: %v", fastErr, fallbackErr)), nil
		}
	}

	if len(elements) == 0 {
		return mcp.NewToolResultError("no UI elements found"), nil
	}

	args := req.GetArguments()

	// Search by index
	if idx, ok := args["index"].(float64); ok {
		idxInt := int(idx)
		for _, el := range elements {
			if el.Index == idxInt {
				sx, sy := sess.ScaleToDevice(el.Center[0], el.Center[1])
				if err := sess.Tap(sx, sy); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("Tapped element [%d] at %v", idxInt, el.Center)), nil
			}
		}
		return mcp.NewToolResultError(fmt.Sprintf("element with index %d not found", idxInt)), nil
	}

	// Search by text
	if text, ok := args["text"].(string); ok && text != "" {
		textLower := strings.ToLower(text)
		for _, el := range elements {
			if strings.Contains(strings.ToLower(el.Text), textLower) || strings.Contains(strings.ToLower(el.ContentDesc), textLower) {
				sx, sy := sess.ScaleToDevice(el.Center[0], el.Center[1])
				if err := sess.Tap(sx, sy); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("Tapped '%s' at %v", text, el.Center)), nil
			}
		}
		return mcp.NewToolResultError(fmt.Sprintf("element with text '%s' not found", text)), nil
	}

	return mcp.NewToolResultError("specify index=N or text='...'"), nil
}

func (s *Server) handleSwipe(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	sx, sxOk := args["start_x"].(float64)
	sy, syOk := args["start_y"].(float64)
	ex, exOk := args["end_x"].(float64)
	ey, eyOk := args["end_y"].(float64)
	if !sxOk || !syOk || !exOk || !eyOk {
		return mcp.NewToolResultError("missing required parameters: start_x, start_y, end_x, end_y"), nil
	}
	startX, startY := int(sx), int(sy)
	endX, endY := int(ex), int(ey)
	duration := 500
	if d, ok := args["duration_ms"].(float64); ok {
		duration = int(d)
	}

	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := sess.Swipe(startX, startY, endX, endY, duration); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(
		fmt.Sprintf("Swiped from (%d, %d) to (%d, %d)", startX, startY, endX, endY)), nil
}

func (s *Server) handleTypeText(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := sess.TypeText(text); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Typed: %s", text)), nil
}

func (s *Server) handleBack(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := sess.Back(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("Back pressed"), nil
}

func (s *Server) handleHome(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := sess.Home(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("Home pressed"), nil
}

func (s *Server) handlePressKey(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	var keycode int
	var label string

	switch {
	case args["keycode"] != nil:
		kc, ok := args["keycode"].(float64)
		if !ok {
			return mcp.NewToolResultError("keycode must be a number"), nil
		}
		keycode = int(kc)
		label = fmt.Sprintf("%d", keycode)
	case args["key"] != nil:
		keyName, ok := args["key"].(string)
		if !ok {
			return mcp.NewToolResultError("key must be a string"), nil
		}
		keycode = protocol.KeycodeFromName(strings.ToLower(strings.TrimSpace(keyName)))
		if keycode == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("unknown key name: %q", keyName)), nil
		}
		label = keyName
	default:
		return mcp.NewToolResultError("keycode or key is required"), nil
	}

	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := sess.PressKey(keycode); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Key %s pressed", label)), nil
}

func (s *Server) handleLaunchApp(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	appName, _ := args["app"].(string)
	if appName == "" {
		appName, _ = args["package"].(string)
	}
	if appName == "" {
		return mcp.NewToolResultError("app or package is required"), nil
	}

	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := sess.LaunchApp(appName); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Launched: %s", appName)), nil
}

func (s *Server) handleWait(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	duration := 1000
	args := req.GetArguments()
	if d, ok := args["duration_ms"].(float64); ok {
		duration = int(d)
	}
	time.Sleep(time.Duration(duration) * time.Millisecond)
	return mcp.NewToolResultText(fmt.Sprintf("Waited %dms", duration)), nil
}

func (s *Server) handleObserve(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sess, err := s.needSession()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	maxShow := getMaxElements(req, 100)
	collectMax := maxShow
	if collectMax < 0 || collectMax > 500 {
		collectMax = 0 // server default (500 for full, 100 for summary)
	}
	isSummary := getSummary(req)
	pngData, elements, err := sess.Observe(collectMax, isSummary)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Return a multi-content result: native ImageContent (the screenshot)
	// plus a TextContent with the formatted UI element list. This lets
	// multimodal LLMs see the screen AND the interactive elements in one
	// atomic call.
	b64 := base64.StdEncoding.EncodeToString(pngData)
	result := mcp.NewToolResultImage(
		fmt.Sprintf("Observe: %d interactive elements", len(elements)),
		b64,
		"image/png",
	)
	result.Content = append(result.Content, mcp.TextContent{
		Type: mcp.ContentTypeText,
		Text: formatElementsForLLM(elements, maxShow, isSummary),
	})
	return result, nil
}

// Session returns the underlying session (used by runCmd in main.go).
func (s *Server) Session() *session.Session { return s.getSession() }

// ── Backward-compatible tool methods (used by runCmd CLI mode) ──

func (s *Server) ListDevices() (*ToolResult, error) {
	r, _ := s.handleListDevices(context.Background(), mcp.CallToolRequest{})
	return mcpResultToToolResult(r), nil
}
func (s *Server) Screenshot() (*ToolResult, error) {
	r, _ := s.handleScreenshot(context.Background(), mcp.CallToolRequest{})
	return mcpResultToToolResult(r), nil
}
func (s *Server) GetUIElements() (*ToolResult, error) {
	r, _ := s.handleGetUIElements(context.Background(), mcp.CallToolRequest{})
	return mcpResultToToolResult(r), nil
}
func (s *Server) Tap(args map[string]interface{}) (*ToolResult, error) {
	r, _ := s.handleTap(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	})
	return mcpResultToToolResult(r), nil
}
func (s *Server) TapElement(args map[string]interface{}) (*ToolResult, error) {
	r, _ := s.handleTapElement(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	})
	return mcpResultToToolResult(r), nil
}
func (s *Server) Swipe(args map[string]interface{}) (*ToolResult, error) {
	r, _ := s.handleSwipe(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	})
	return mcpResultToToolResult(r), nil
}
func (s *Server) TypeText(args map[string]interface{}) (*ToolResult, error) {
	r, _ := s.handleTypeText(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	})
	return mcpResultToToolResult(r), nil
}
func (s *Server) Back() (*ToolResult, error) {
	r, _ := s.handleBack(context.Background(), mcp.CallToolRequest{})
	return mcpResultToToolResult(r), nil
}
func (s *Server) Home() (*ToolResult, error) {
	r, _ := s.handleHome(context.Background(), mcp.CallToolRequest{})
	return mcpResultToToolResult(r), nil
}
func (s *Server) PressKey(args map[string]interface{}) (*ToolResult, error) {
	r, _ := s.handlePressKey(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	})
	return mcpResultToToolResult(r), nil
}
func (s *Server) LaunchApp(args map[string]interface{}) (*ToolResult, error) {
	r, _ := s.handleLaunchApp(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	})
	return mcpResultToToolResult(r), nil
}
func (s *Server) Wait(args map[string]interface{}) (*ToolResult, error) {
	r, _ := s.handleWait(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	})
	return mcpResultToToolResult(r), nil
}
func (s *Server) Observe() (*ToolResult, error) {
	r, _ := s.handleObserve(context.Background(), mcp.CallToolRequest{})
	return mcpResultToToolResult(r), nil
}

func mcpResultToToolResult(r *mcp.CallToolResult) *ToolResult {
	if r == nil {
		return errorResult(fmt.Errorf("tool returned nil"))
	}
	var content []ToolContent
	for _, c := range r.Content {
		tc := ToolContent{}
		// Type-assert to concrete content types
		switch ct := c.(type) {
		case mcp.TextContent:
			tc.Type = ct.Type
			tc.Text = ct.Text
		case mcp.ImageContent:
			tc.Type = ct.Type
			tc.Data = ct.Data
			tc.MimeType = ct.MIMEType
		default:
			// Fallback: use the content as-is via JSON roundtrip
			data, _ := json.Marshal(ct)
			tc.Type = "text"
			tc.Text = string(data)
		}
		content = append(content, tc)
	}
	return &ToolResult{Content: content}
}

// ── Formatting helpers ──

func formatElementsForLLM(elements []protocol.UIElement, maxShow int, isSummary bool) string {
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
			if idx := strings.LastIndex(simpleID, "/"); idx >= 0 {
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
				if idx := strings.LastIndex(cn, "."); idx >= 0 {
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

// getMaxElements extracts the max_elements argument from an MCP CallToolRequest.
// Returns the provided value (clamped to valid range) or the default if not set.
func getMaxElements(req mcp.CallToolRequest, defaultVal int) int {
	args := req.GetArguments()
	if v, ok := args["max_elements"].(float64); ok {
		n := int(v)
		if n < 0 {
			return -1 // show all
		}
		if n == 0 {
			return defaultVal
		}
		return n
	}
	return defaultVal
}

// getSummary extracts the summary boolean from an MCP CallToolRequest.
func getSummary(req mcp.CallToolRequest) bool {
	args := req.GetArguments()
	if v, ok := args["summary"].(bool); ok {
		return v
	}
	return false
}
