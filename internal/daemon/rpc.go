package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/internal/session"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// ── JSON-RPC 2.0 types ──

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      int64           `json:"id"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      int64           `json:"id"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC error codes.
const (
	ErrParse    = -32700
	ErrMethod   = -32601
	ErrInvalid  = -32602
	ErrInternal = -32603
	ErrDevice   = -32000
	ErrNoDevice = -32001
	ErrTimeout  = -32002
)

func newErrorResponse(id int64, code int, msg string) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
}

func newResultResponse(id int64, result any) *Response {
	data, _ := json.Marshal(result)
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  data,
	}
}

// ── Params helpers ──

func getFloat(params map[string]any, key string) float64 {
	v, _ := params[key].(float64)
	return v
}

func getInt(params map[string]any, key string) int {
	return int(getFloat(params, key))
}

func getString(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

func parseIntParam(raw json.RawMessage, key string) int {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return 0
	}
	return getInt(params, key)
}

func parseStringParam(raw json.RawMessage, key string) string {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return ""
	}
	return getString(params, key)
}

func parseParams(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	return params, nil
}

// ── Dispatch ──

// Dispatch routes a JSON-RPC request to the appropriate handler on the
// current session. The session must be non-nil for all methods except "status".
func Dispatch(sess *session.Session, req *Request) *Response {
	phonelog.Default().Write("rpc %s", req.Method)
	switch req.Method {
	case "status":
		return handleStatus(sess, req)

	case "connect":
		return newErrorResponse(req.ID, ErrInternal, "connect requires daemon-level reconnect; use daemon --stop then daemon")

	case "disconnect":
		return newErrorResponse(req.ID, ErrInternal, "disconnect requires daemon-level shutdown; use daemon --stop")

	case "list_devices":
		return handleListDevices(sess, req)

	case "screenshot":
		return handleScreenshot(sess, req)

	case "get_ui_elements":
		return handleGetUIElements(sess, req)

	case "observe":
		return handleObserve(sess, req)

	case "tap":
		return handleTap(sess, req)

	case "tap_element":
		return handleTapElement(sess, req)

	case "swipe":
		return handleSwipe(sess, req)

	case "type_text":
		return handleTypeText(sess, req)

	case "back":
		return handleBack(sess, req)

	case "home":
		return handleHome(sess, req)

	case "press_key":
		return handlePressKey(sess, req)

	case "launch_app":
		return handleLaunchApp(sess, req)

	case "wait":
		return handleWait(req)

	default:
		return newErrorResponse(req.ID, ErrMethod, fmt.Sprintf("unknown method: %s", req.Method))
	}
}

// ── Handlers ──

func handleStatus(sess *session.Session, req *Request) *Response {
	status := map[string]any{
		"connected": false,
		"pid":       float64(os.Getpid()),
	}
	if sess != nil {
		alive := sess.IsAlive()
		status["connected"] = alive
		status["serial"] = sess.Serial
		status["device_width"] = float64(sess.DeviceW)
		status["device_height"] = float64(sess.DeviceH)
		status["control_available"] = sess.IsControlAvailable()
		status["ui_available"] = sess.IsUIAvailable()
	}
	return newResultResponse(req.ID, status)
}

func handleListDevices(sess *session.Session, req *Request) *Response {
	type deviceInfo struct {
		Serial string `json:"serial"`
		Model  string `json:"model,omitempty"`
		Status string `json:"status"`
	}

	// If daemon has a connected session, return it directly (no ADB call needed)
	if sess != nil {
		return newResultResponse(req.ID, []deviceInfo{{
			Serial: sess.Serial,
			Status: "device",
		}})
	}

	// Fallback: scan via ADB
	devices, err := adb.ListDevices()
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	var list []deviceInfo
	for _, d := range devices {
		list = append(list, deviceInfo{
			Serial: d.Serial,
			Model:  d.Model,
			Status: d.Status,
		})
	}
	return newResultResponse(req.ID, list)
}

func handleScreenshot(sess *session.Session, req *Request) *Response {
	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	pngData, w, h, err := sess.Screenshot()
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("screenshot: %v", err))
	}

	b64 := base64.StdEncoding.EncodeToString(pngData)
	return newResultResponse(req.ID, map[string]any{
		"text":       fmt.Sprintf("Screenshot (%dx%d)", w, h),
		"image_data": b64,
		"mime_type":  "image/png",
	})
}

func handleGetUIElements(sess *session.Session, req *Request) *Response {
	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	maxShow := getMaxElementsFromParams(req, 100)
	collectMax := maxShow
	if collectMax < 0 || collectMax > 500 {
		collectMax = 0 // server default (500 for full, 100 for summary)
	}
	isSummary := getSummaryFromParams(req)
	var elements []protocol.UIElement
	var err error
	if isSummary {
		elements, err = sess.GetUISummary(collectMax)
	} else {
		elements, err = sess.GetUIElements(collectMax)
	}
	if err != nil {
		elements, err = sess.GetUIElementsFallbackADB(collectMax)
		if err != nil {
			return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("get ui elements: %v", err))
		}
	}

	formatted := formatElementsForLLM(elements, maxShow, isSummary)
	return newResultResponse(req.ID, map[string]any{
		"elements":  elements,
		"formatted": formatted,
		"count":     len(elements),
	})
}

func handleObserve(sess *session.Session, req *Request) *Response {
	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	maxShow := getMaxElementsFromParams(req, 100)
	collectMax := maxShow
	if collectMax < 0 || collectMax > 500 {
		collectMax = 0 // server default (500 for full, 100 for summary)
	}
	isSummary := getSummaryFromParams(req)
	pngData, elements, err := sess.Observe(collectMax, isSummary)
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("observe: %v", err))
	}

	b64 := base64.StdEncoding.EncodeToString(pngData)
	formatted := formatElementsForLLM(elements, maxShow, isSummary)

	return newResultResponse(req.ID, map[string]any{
		"text":       formatted,
		"image_data": b64,
		"mime_type":  "image/png",
	})
}

func handleTap(sess *session.Session, req *Request) *Response {
	params, err := parseParams(req.Params)
	if err != nil {
		return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("invalid params: %v", err))
	}

	if _, ok := params["x"]; !ok {
		return newErrorResponse(req.ID, ErrInvalid, "missing required parameter: x")
	}
	if _, ok := params["y"]; !ok {
		return newErrorResponse(req.ID, ErrInvalid, "missing required parameter: y")
	}
	x := getInt(params, "x")
	y := getInt(params, "y")

	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	sx, sy := sess.ScaleToDevice(x, y)
	if err := sess.Tap(sx, sy); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": fmt.Sprintf("Tapped at (%d, %d)", x, y),
	})
}

func handleTapElement(sess *session.Session, req *Request) *Response {
	params, err := parseParams(req.Params)
	if err != nil {
		return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("invalid params: %v", err))
	}

	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	elements, fastErr := sess.GetUIElements(0) // collect all elements (server default 500)
	if fastErr != nil {
		var fallbackErr error
		elements, fallbackErr = sess.GetUIElementsFallbackADB(0)
		if fallbackErr != nil {
			return newErrorResponse(req.ID, ErrDevice,
				fmt.Sprintf("ui dump failed: %v; adb fallback: %v", fastErr, fallbackErr))
		}
	}

	if len(elements) == 0 {
		return newErrorResponse(req.ID, ErrDevice, "no UI elements found")
	}

	// Search by index
	if _, ok := params["index"]; ok {
		idx := getInt(params, "index")
		for _, el := range elements {
			if el.Index == idx {
				sx, sy := sess.ScaleToDevice(el.Center[0], el.Center[1])
				if err := sess.Tap(sx, sy); err != nil {
					return newErrorResponse(req.ID, ErrDevice, err.Error())
				}
				return newResultResponse(req.ID, map[string]any{
					"message": fmt.Sprintf("Tapped element [%d] at %v", idx, el.Center),
				})
			}
		}
		return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("element with index %d not found", idx))
	}

	// Search by text
	if text := getString(params, "text"); text != "" {
		textLower := toLower(text)
		for _, el := range elements {
			if contains(toLower(el.Text), textLower) || contains(toLower(el.ContentDesc), textLower) {
				sx, sy := sess.ScaleToDevice(el.Center[0], el.Center[1])
				if err := sess.Tap(sx, sy); err != nil {
					return newErrorResponse(req.ID, ErrDevice, err.Error())
				}
				return newResultResponse(req.ID, map[string]any{
					"message": fmt.Sprintf("Tapped '%s' at %v", text, el.Center),
				})
			}
		}
		return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("element with text '%s' not found", text))
	}

	return newErrorResponse(req.ID, ErrInvalid, "specify index=N or text='...'")
}

func handleSwipe(sess *session.Session, req *Request) *Response {
	params, err := parseParams(req.Params)
	if err != nil {
		return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("invalid params: %v", err))
	}

	startX := getInt(params, "start_x")
	startY := getInt(params, "start_y")
	endX := getInt(params, "end_x")
	endY := getInt(params, "end_y")
	duration := getInt(params, "duration_ms")
	if duration == 0 {
		duration = 500
	}

	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	sx1, sy1 := sess.ScaleToDevice(startX, startY)
	sx2, sy2 := sess.ScaleToDevice(endX, endY)
	if err := sess.Swipe(sx1, sy1, sx2, sy2, duration); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": fmt.Sprintf("Swiped from (%d, %d) to (%d, %d)", startX, startY, endX, endY),
	})
}

func handleTypeText(sess *session.Session, req *Request) *Response {
	text := parseStringParam(req.Params, "text")
	if text == "" {
		return newErrorResponse(req.ID, ErrInvalid, "missing required parameter: text")
	}

	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	if err := sess.TypeText(text); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": fmt.Sprintf("Typed: %s", text),
	})
}

func handleBack(sess *session.Session, req *Request) *Response {
	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	if err := sess.Back(); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": "Back pressed",
	})
}

func handleHome(sess *session.Session, req *Request) *Response {
	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	if err := sess.Home(); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": "Home pressed",
	})
}

func handlePressKey(sess *session.Session, req *Request) *Response {
	params, err := parseParams(req.Params)
	if err != nil {
		return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("invalid params: %v", err))
	}

	// Try keycode first, then key name
	if _, ok := params["keycode"]; ok {
		keycode := getInt(params, "keycode")
		if sess == nil {
			return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
		}
		if err := sess.PressKey(keycode); err != nil {
			return newErrorResponse(req.ID, ErrDevice, err.Error())
		}
		return newResultResponse(req.ID, map[string]any{
			"message": fmt.Sprintf("Key %d pressed", keycode),
		})
	}

	if keyName, ok := params["key"].(string); ok {
		kc := protocol.KeycodeFromName(keyName)
		if kc == 0 {
			return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("unknown key name: %q", keyName))
		}
		if sess == nil {
			return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
		}
		if err := sess.PressKey(int(kc)); err != nil {
			return newErrorResponse(req.ID, ErrDevice, err.Error())
		}
		return newResultResponse(req.ID, map[string]any{
			"message": fmt.Sprintf("Key %d pressed", kc),
		})
	}

	return newErrorResponse(req.ID, ErrInvalid, "keycode or key is required")
}

func handleLaunchApp(sess *session.Session, req *Request) *Response {
	appName := parseStringParam(req.Params, "package")
	if appName == "" {
		appName = parseStringParam(req.Params, "app")
	}
	if appName == "" {
		return newErrorResponse(req.ID, ErrInvalid, "app or package is required")
	}

	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	if err := sess.LaunchApp(appName); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": fmt.Sprintf("Launched: %s", appName),
	})
}

func handleWait(req *Request) *Response {
	duration := parseIntParam(req.Params, "duration_ms")
	if duration == 0 {
		duration = 1000
	}

	time.Sleep(time.Duration(duration) * time.Millisecond)

	return newResultResponse(req.ID, map[string]any{
		"message": fmt.Sprintf("Waited %dms", duration),
	})
}

// ── Formatting helpers (mirrored from internal/mcp/tools.go) ──

func formatElementsForLLM(elements []protocol.UIElement, maxShow int, isSummary bool) string {
	if len(elements) == 0 {
		return "No interactive elements found on screen."
	}

	if maxShow < 0 || maxShow > len(elements) {
		maxShow = len(elements)
	}

	var lines []string
	lines = append(lines, "Interactive elements on screen:")
	lines = append(lines, "="+repeatString("=", 49))

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
			if idx := lastIndexByte(simpleID, '/'); idx >= 0 {
				simpleID = simpleID[idx+1:]
			}
			parts = append(parts, fmt.Sprintf(`id="%s"`, simpleID))
		}
		if el.ClassName != "" {
			cn := el.ClassName
			if idx := lastIndexByte(cn, '.'); idx >= 0 {
				cn = cn[idx+1:]
			}
			parts = append(parts, fmt.Sprintf("(%s)", cn))
		}
		if el.Clickable {
			parts = append(parts, "[clickable]")
		}

		desc := joinStrings(parts, " ")
		if desc == "" {
			desc = fmt.Sprintf("(%s)", el.ClassName)
		}
		lines = append(lines, fmt.Sprintf("[%d] %s bounds=[%d,%d][%d,%d]",
			el.Index, desc,
			el.Bounds[0], el.Bounds[1], el.Bounds[2], el.Bounds[3]))
	}

	lines = append(lines, "="+repeatString("=", 49))
	lines = append(lines, "Use tap_element with index=N or text='...' to interact.")

	return joinStrings(lines, "\n")
}

func getMaxElementsFromParams(req *Request, defaultVal int) int {
	params, err := parseParams(req.Params)
	if err != nil {
		return defaultVal
	}
	if v, ok := params["max_elements"].(float64); ok {
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

func getSummaryFromParams(req *Request) bool {
	params, err := parseParams(req.Params)
	if err != nil {
		return false
	}
	v, ok := params["summary"].(bool)
	return ok && v
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func contains(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func repeatString(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}
