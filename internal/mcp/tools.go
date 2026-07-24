package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

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
				mcp.WithString("format",
					mcp.Description("Output format: 'flat' (default), 'jsonl', 'simplexml', 'flatref', 'yml'."),
				),
				mcp.WithBoolean("summary",
					mcp.Description("If true, filter out layout containers (flat format only)."),
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
//
// Every handler routes through the unified daemon via rpcCall instead of
// touching a session directly. The daemon injects `device=serial` into each
// request (daemon.Client.Call), so device isolation is handled centrally.
// Result field names match the daemon RPC handlers in internal/daemon/rpc.go.

func (s *Server) handleListDevices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// list_devices is a connectionless RPC — no device binding, returns all
	// ADB-visible devices. The daemon reports every device, not just the
	// bound one (see handleListDevices in rpc.go).
	var list []map[string]any
	if err := s.rpcCall("list_devices", nil, &list); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, _ := json.Marshal(list)
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleScreenshot(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var resp struct {
		Text      string `json:"text"`
		ImageData string `json:"image_data"`
		MimeType  string `json:"mime_type"`
	}
	if err := s.rpcCall("screenshot", nil, &resp); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	caption := resp.Text
	if caption == "" {
		caption = "Screenshot"
	}
	mime := resp.MimeType
	if mime == "" {
		mime = "image/png"
	}
	return mcp.NewToolResultImage(caption, resp.ImageData, mime), nil
}

func (s *Server) handleGetUIElements(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := map[string]any{
		"max_elements": getMaxElements(req, 100),
		"summary":      getSummary(req),
		"format":       getFormat(req),
	}
	var resp struct {
		Formatted string `json:"formatted"`
		Count     int    `json:"count"`
	}
	if err := s.rpcCall("get_ui_elements", params, &resp); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if resp.Formatted == "" {
		return mcp.NewToolResultText(fmt.Sprintf("No interactive elements found (%d total).", resp.Count)), nil
	}
	return mcp.NewToolResultText(resp.Formatted), nil
}

func (s *Server) handleTap(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	xVal, xOk := args["x"].(float64)
	yVal, yOk := args["y"].(float64)
	if !xOk || !yOk {
		return mcp.NewToolResultError("missing required parameters: x and y"), nil
	}
	return s.simpleMessageAction("tap",
		map[string]any{"x": int(xVal), "y": int(yVal)},
		fmt.Sprintf("Tapped at (%d, %d)", int(xVal), int(yVal)))
}

func (s *Server) handleTapElement(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	params := map[string]any{}
	if idx, ok := args["index"].(float64); ok {
		params["index"] = int(idx)
	} else if text, ok := args["text"].(string); ok && text != "" {
		params["text"] = text
	} else {
		return mcp.NewToolResultError("specify index or text"), nil
	}
	return s.simpleMessageAction("tap_element", params, "Tapped element")
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
	params := map[string]any{
		"start_x": int(sx), "start_y": int(sy),
		"end_x": int(ex), "end_y": int(ey),
	}
	if d, ok := args["duration_ms"].(float64); ok {
		params["duration_ms"] = int(d)
	}
	return s.simpleMessageAction("swipe", params,
		fmt.Sprintf("Swiped from (%d, %d) to (%d, %d)", int(sx), int(sy), int(ex), int(ey)))
}

func (s *Server) handleTypeText(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return s.simpleMessageAction("type_text", map[string]any{"text": text}, fmt.Sprintf("Typed: %s", text))
}

func (s *Server) handleBack(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.simpleAction("back", "Back pressed")
}

func (s *Server) handleHome(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.simpleAction("home", "Home pressed")
}

func (s *Server) handlePressKey(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	params := map[string]any{}
	var label string
	switch {
	case args["keycode"] != nil:
		kc, ok := args["keycode"].(float64)
		if !ok {
			return mcp.NewToolResultError("keycode must be a number"), nil
		}
		params["keycode"] = int(kc)
		label = fmt.Sprintf("%d", int(kc))
	case args["key"] != nil:
		keyName, ok := args["key"].(string)
		if !ok {
			return mcp.NewToolResultError("key must be a string"), nil
		}
		// Resolve the keycode locally so an unknown key name is rejected
		// before round-tripping to the daemon.
		kc := protocol.KeycodeFromName(strings.ToLower(strings.TrimSpace(keyName)))
		if kc == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("unknown key name: %q", keyName)), nil
		}
		params["keycode"] = int(kc)
		label = keyName
	default:
		return mcp.NewToolResultError("keycode or key is required"), nil
	}
	return s.simpleMessageAction("press_key", params, fmt.Sprintf("Key %s pressed", label))
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
	return s.simpleMessageAction("launch_app", map[string]any{"package": appName}, fmt.Sprintf("Launched: %s", appName))
}

func (s *Server) handleWait(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	duration := 1000
	args := req.GetArguments()
	if d, ok := args["duration_ms"].(float64); ok {
		duration = int(d)
	}
	// Local sleep — no device round-trip. Routing through the daemon would run
	// time.Sleep on the device actor's single-threaded event loop, blocking
	// every other request to that device (and the 10s health ticker) for the
	// full duration. wait has no device-side effect, so sleep here instead.
	time.Sleep(time.Duration(duration) * time.Millisecond)
	return mcp.NewToolResultText(fmt.Sprintf("Waited %dms", duration)), nil
}

func (s *Server) handleObserve(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := map[string]any{
		"max_elements": getMaxElements(req, 100),
		"summary":      getSummary(req),
	}
	var resp struct {
		Text      string `json:"text"`
		ImageData string `json:"image_data"`
		MimeType  string `json:"mime_type"`
		Count     int    `json:"count"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	}
	if err := s.rpcCall("observe", params, &resp); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Caption is a short summary; resp.Text holds the full element list and is
	// emitted as a separate TextContent below — do NOT reuse it as the caption
	// (that would send the element list twice).
	caption := fmt.Sprintf("Observe: %d interactive elements", resp.Count)
	mime := resp.MimeType
	if mime == "" {
		mime = "image/png"
	}
	// Multi-content: image + formatted element list in one atomic result.
	result := mcp.NewToolResultImage(caption, resp.ImageData, mime)
	if resp.Text != "" {
		result.Content = append(result.Content, mcp.TextContent{
			Type: mcp.ContentTypeText,
			Text: resp.Text,
		})
	}
	return result, nil
}

// simpleAction dispatches a parameter-less action (back/home) and returns the
// daemon's message (or a fallback label).
func (s *Server) simpleAction(method, fallback string) (*mcp.CallToolResult, error) {
	return s.simpleMessageAction(method, nil, fallback)
}

// simpleMessageAction dispatches an RPC that returns a {"message":...} result,
// returning that message or a fallback label. Most device actions (tap, swipe,
// back, type_text, press_key, launch_app, …) share this shape.
func (s *Server) simpleMessageAction(method string, params map[string]any, fallback string) (*mcp.CallToolResult, error) {
	var resp struct {
		Message string `json:"message"`
	}
	if err := s.rpcCall(method, params, &resp); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(orDefault(resp.Message, fallback)), nil
}

// orDefault returns s if non-empty, else fallback.
func orDefault(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

// getFormat extracts the format argument from an MCP CallToolRequest.
func getFormat(req mcp.CallToolRequest) string {
	args := req.GetArguments()
	if v, ok := args["format"].(string); ok {
		return strings.ToLower(strings.TrimSpace(v))
	}
	return ""
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
