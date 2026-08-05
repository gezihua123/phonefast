package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder for Screenshot() fallback output
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	"github.com/gezihua123/phonefast/internal/format"
	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/internal/session"
	"github.com/gezihua123/phonefast/pkg/avcodec"
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

	// Streaming image responses (screenshot/observe): streamPrefix and
	// streamSuffix are the JSON frame parts around the base64 payload value;
	// writeStreamedResponse emits them with the payload base64-encoded
	// incrementally in between. This avoids materializing a megabyte-scale
	// base64 string plus two json.Marshal copies of it.
	// All three fields are nil for normal (non-streaming) responses.
	// Write-path only: json.Marshal on a streaming Response produces an
	// incomplete result (Result is nil); only writeResponse/writeStreamedResponse
	// can serialize it correctly.
	streamPrefix  json.RawMessage
	streamSuffix  json.RawMessage
	streamPayload []byte
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

// newStreamedImageResponse builds a Response whose Result contains a large
// base64 image field, without copying the payload or using a fragile marker
// string. Instead, the non-image fields are marshaled separately, then the
// image_data key is spliced in manually. This eliminates the risk of a field
// value colliding with a marker string.
//
// Wire format: {"jsonrpc":"2.0","result":{...nonImage,"image_data":"<b64>"},"id":N}
func newStreamedImageResponse(id int64, m map[string]any, b64Field string, payload []byte) *Response {
	// Remove the image field, marshal the rest.
	delete(m, b64Field)
	nonImage, err := json.Marshal(m)
	if err != nil {
		return newErrorResponse(id, ErrInternal, fmt.Sprintf("marshal result: %v", err))
	}

	// Build: nonImage is `{"text":"...","mime_type":"image/png"}`.
	// Strip the closing '}' and append ,"image_data":" to get the prefix.
	// The suffix is `"}` to close both the string and the result object.
	rest := nonImage[:len(nonImage)-1] // strip '}'
	prefix := make([]byte, 0, len(rest)+len(b64Field)+5)
	prefix = append(prefix, rest...)
	prefix = append(prefix, `,"`...)
	prefix = append(prefix, b64Field...)
	prefix = append(prefix, `":"`...)

	return &Response{
		JSONRPC:      "2.0",
		ID:           id,
		streamPrefix: prefix,
		streamSuffix: []byte{'"', '}'},
		streamPayload: payload,
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
// current session. The session must be non-nil for all methods except
// "status". Daemon-level methods ("connect", "disconnect") are handled
// in handleConn, not here — they should never reach Dispatch in normal
// operation; the cases here are defensive fallbacks.
func Dispatch(sess *session.Session, req *Request) *Response {
	phonelog.Default().Write("rpc %s", req.Method)
	switch req.Method {
	case "status":
		return handleStatus(sess, req)

	case "connect":
		return newErrorResponse(req.ID, ErrInternal, "connect is a daemon-level operation; use phonefast daemon connect <serial>")

	case "disconnect":
		return newErrorResponse(req.ID, ErrInternal, "disconnect is a daemon-level operation; use phonefast daemon disconnect <serial>")

	case "list_devices":
		return handleListDevices(sess, req)

	case "screenshot":
		return handleScreenshot(sess, req)

	case "get_ui_elements":
		return handleGetUIElements(sess, req)

	case "observe":
		return handleObserve(sess, req)

	case "ocr":
		return handleOCR(sess, req)

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

	// "wait" is deliberately NOT a daemon RPC: it has no device-side effect,
	// and a daemon-side time.Sleep would block the device actor's single-
	// threaded event loop (stalling every other request + the health ticker
	// for the full duration). Callers sleep locally (waitCmd, runDaemonAction,
	// mcp handleWait). A raw RPC "wait" returns method-not-found by design.

	default:
		return newErrorResponse(req.ID, ErrMethod, fmt.Sprintf("unknown method: %s", req.Method))
	}
}

// ── Handlers ──

// handleStatus reports a single device session's status. When called via the
// daemon's handleConn, sess is always non-nil: status-with-no-device is handled
// upstream by writeDaemonStatus (daemon-level info). The nil branch is kept as
// a defensive fallback for other Dispatch callers (e.g. direct invocation).
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

// handleListDevices lists ALL connected devices via ADB, independent of the
// per-request session. handleConn binds a session for non-status methods, so
// the old "if sess != nil" early return would have made this only ever report
// the single bound device. list_devices must see every device.
func handleListDevices(sess *session.Session, req *Request) *Response {
	type deviceInfo struct {
		Serial string `json:"serial"`
		Model  string `json:"model,omitempty"`
		Status string `json:"status"`
	}

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

	// Request JPEG directly from the CGO decoder (avoids ~4.6MB image.Decode
	// allocation that pngToJPEG would incur on the fallback PNG path).
	imgData, w, h, mime, err := sess.ScreenshotFormat(avcodec.FormatJPEG)
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("screenshot: %v", err))
	}

	// CLI fallback (ffmpeg) always returns PNG regardless of format request.
	// Convert to JPEG for smaller MCP payload (~10× vs PNG at native res).
	if mime != "image/jpeg" {
		jpgData, jpgErr := pngToJPEG(imgData, 85)
		if jpgErr != nil {
			phonelog.Default().Write("screenshot: png→jpeg failed, falling back: %v", jpgErr)
			jpgData = imgData
		}
		imgData = jpgData
	}

	return newStreamedImageResponse(req.ID, map[string]any{
		"text":      fmt.Sprintf("Screenshot (%dx%d)", w, h),
		"mime_type": "image/jpeg",
	}, "image_data", imgData)
}

func handleGetUIElements(sess *session.Session, req *Request) *Response {
	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	formatType := getFormatFromParams(req)
	maxShow := getMaxElementsFromParams(req, 5000)
	collectMax := maxShow
	if collectMax < 0 || collectMax > protocol.DefMaxElements {
		collectMax = 0 // server default (DefMaxElements)
	}

	// Handle hierarchical formats via UIFormatter registry
	if f := format.ByName(formatType); f != nil {
		fullElements, err := sess.GetUIFull(collectMax)
		if err != nil {
			return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("get ui full: %v", err))
		}
		if maxShow > 0 && len(fullElements) > maxShow {
			fullElements = fullElements[:maxShow]
		}

		formatted := f.Format(fullElements)
		return newResultResponse(req.ID, map[string]any{
			"elements":  fullElements,
			"formatted": formatted,
			"count":     len(fullElements),
			"format":    formatType,
		})
	}

	// Legacy flat format (no format specified or unknown format).
	// Always summary mode: the flat path returns filtered UIElement[]
	// (layout containers / pure images skipped). Full unfiltered mode
	// requires a hierarchical format, handled above via GetUIFull.
	elements, err := sess.GetUISummary(collectMax)
	if err != nil {
		elements, err = sess.GetUIElementsFallbackADB(collectMax)
		if err != nil {
			return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("get ui elements: %v", err))
		}
	}

	// Collapse off-screen elements for token-efficient output.
	// Use NativeW×NativeH for the viewport: UI element bounds come from
	// AccessibilityNodeInfo.getBoundsInScreen(), which reports coordinates
	// in the physical display space, NOT the scrcpy video resolution
	// (DeviceW×DeviceH). On devices where the two differ (e.g. a 1080×2400
	// display whose scrcpy video is negotiated to 488×1080), using
	// DeviceW/H as the viewport would incorrectly classify every element
	// beyond the video boundary as off-screen.
	vw, vh := 0, 0
	if sess.NativeW > 0 && sess.NativeH > 0 {
		vw, vh = sess.NativeW, sess.NativeH
	}
	legacyFormatted := format.ElementsForLLMWithViewport(elements, maxShow, true, vw, vh)
	return newResultResponse(req.ID, map[string]any{
		"elements":  elements,
		"formatted": legacyFormatted,
		"count":     len(elements),
	})
}

func handleObserve(sess *session.Session, req *Request) *Response {
	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	formatType := getFormatFromParams(req)
	if formatType == "" {
		formatType = "flatref"
	}
	maxShow := getMaxElementsFromParams(req, 5000)
	collectMax := maxShow
	if collectMax < 0 || collectMax > protocol.DefMaxElements {
		collectMax = 0 // server default (DefMaxElements)
	}
	isSummary := getSummaryFromParams(req)

	// Hierarchical formats (flatref, jsonl, simplexml, yml): need full UI tree.
	// Run screenshot + GetUIFull concurrently; each has its own 30s timeout.
	if f := format.ByName(formatType); f != nil {
		type screenRes struct {
			img  []byte
			mime string
			err  error
		}
		type uiRes struct {
			elements []protocol.UIFullElement
			err      error
		}
		scCh := make(chan screenRes, 1)
		uiCh := make(chan uiRes, 1)

		go func() {
			// Request JPEG directly from CGO decoder to avoid ~4.6MB
			// image.Decode allocation that pngToJPEG would otherwise incur.
			img, _, _, mime, err := sess.ScreenshotFormat(avcodec.FormatJPEG)
			scCh <- screenRes{img, mime, err}
		}()
		go func() {
			// Retry once on transient errors (stale-node exceptions during
			// animations are common now that waitForIdle is removed).
			elems, err := sess.GetUIFull(collectMax)
			if err != nil {
				elems, err = sess.GetUIFull(collectMax)
			}
			uiCh <- uiRes{elems, err}
		}()

		var sc screenRes
		select {
		case sc = <-scCh:
		case <-time.After(30 * time.Second):
			return newErrorResponse(req.ID, ErrTimeout, "screenshot timed out")
		}

		var ui uiRes
		select {
		case ui = <-uiCh:
		case <-time.After(30 * time.Second):
			return newErrorResponse(req.ID, ErrTimeout, "get ui elements timed out")
		}

		if sc.err != nil {
			return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("observe screenshot: %v", sc.err))
		}
		if ui.err != nil {
			return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("observe ui: %v", ui.err))
		}

		// Summary mode: keep only the first N elements (already depth-first
		// from the server, so topmost widgets come first).
		if isSummary && maxShow > 50 {
			maxShow = 50
		}
		if maxShow > 0 && len(ui.elements) > maxShow {
			ui.elements = ui.elements[:maxShow]
		}

		formatted := f.Format(ui.elements)

		// CLI fallback (ffmpeg) always returns PNG regardless of format request.
		// Convert to JPEG for ~10x smaller MCP payload only when needed.
		// Native resolution is preserved; OCR is unaffected because
		// it reads decoded video frames, not this screenshot.
		imgData := sc.img
		if sc.mime != "image/jpeg" {
			jpgData, jpgErr := pngToJPEG(sc.img, 85)
			if jpgErr != nil {
				phonelog.Default().Write("observe: png→jpeg failed, falling back: %v", jpgErr)
				jpgData = sc.img
			}
			imgData = jpgData
		}

		return newStreamedImageResponse(req.ID, map[string]any{
			"text":      formatted,
			"mime_type": "image/jpeg",
			"count":     len(ui.elements),
			"width":     sess.DeviceW,
			"height":    sess.DeviceH,
			"format":    formatType,
		}, "image_data", imgData)
	}

	// Legacy flat format (no format specified or unknown format).
	pngData, elements, err := sess.Observe(collectMax, isSummary)
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("observe: %v", err))
	}

	// Collapse off-screen only in summary mode; full mode keeps all elements.
	// Use NativeW×NativeH — see handleGetUIElements for rationale.
	ovw, ovh := 0, 0
	if isSummary && sess.NativeW > 0 && sess.NativeH > 0 {
		ovw, ovh = sess.NativeW, sess.NativeH
	}
	formatted := format.ElementsForLLMWithViewport(elements, maxShow, isSummary, ovw, ovh)

	return newStreamedImageResponse(req.ID, map[string]any{
		"text":      formatted,
		"mime_type": "image/png",
		"count":     len(elements),
		"width":     sess.DeviceW,
		"height":    sess.DeviceH,
	}, "image_data", pngData)
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

	elements, fastErr := sess.GetUISummary(0) // server default (DefMaxElements)
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
		textLower := strings.ToLower(text)
		for _, el := range elements {
			if strings.Contains(strings.ToLower(el.Text), textLower) || strings.Contains(strings.ToLower(el.ContentDesc), textLower) {
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

// ── Params extraction helpers ──

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

func getFormatFromParams(req *Request) string {
	params, err := parseParams(req.Params)
	if err != nil {
		return ""
	}
	v, _ := params["format"].(string)
	return strings.ToLower(strings.TrimSpace(v))
}

func getSummaryFromParams(req *Request) bool {
	params, err := parseParams(req.Params)
	if err != nil {
		return false
	}
	v, ok := params["summary"].(bool)
	return ok && v
}

// jpegBufPool reuses bytes.Buffer allocations across pngToJPEG calls.
// Each buffer is typically ~200-500KB (JPEG-encoded 720×1600 image).
var jpegBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// pngToJPEG decodes a PNG image and re-encodes it as JPEG at the given
// quality (1-100). Used on the CLI ffmpeg fallback path to shrink MCP
// screenshot payloads ~10× without downscaling — native resolution is
// preserved. The primary CGO path returns JPEG directly from the decoder
// and bypasses this function entirely.
func pngToJPEG(png []byte, quality int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		return nil, err
	}
	buf := jpegBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer jpegBufPool.Put(buf)

	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	// Copy out before returning to pool: buf.Bytes() aliases the internal
	// buffer and will be invalidated by the next Get() call.
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	return out, nil
}
