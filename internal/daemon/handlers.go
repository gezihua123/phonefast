package daemon

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gezihua123/phonefast/internal/adb"
	"github.com/gezihua123/phonefast/internal/format"
	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/internal/session"
	ocrsvc "github.com/gezihua123/phonefast/ocr"
	"github.com/gezihua123/phonefast/pkg/avcodec"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// ── Handlers ──

// handleStatus reports a single device session's status. When called via the
// daemon's handleConn, dev is always non-nil: status-with-no-device is handled
// upstream by writeDaemonStatus (daemon-level info). The nil branch is kept as
// a defensive fallback for other Dispatch callers (e.g. direct invocation).
func handleStatus(dev DeviceHealth, req *Request) *Response {
	status := map[string]any{
		"connected": false,
		"pid":       float64(os.Getpid()),
	}
	if dev != nil {
		alive := dev.IsAlive()
		status["connected"] = alive
		status["serial"] = dev.Serial()
		status["device_width"] = float64(dev.DeviceWidth())
		status["device_height"] = float64(dev.DeviceHeight())
		status["control_available"] = dev.IsControlAvailable()
		status["ui_available"] = dev.IsUIAvailable()
	}
	return newResultResponse(req.ID, status)
}

// handleListDevices lists ALL connected devices via ADB, independent of the
// per-request session. handleConn binds a session for non-status methods, so
// the old "if dev != nil" early return would have made this only ever report
// the single bound device. list_devices must see every device.
func handleListDevices(dev Device, req *Request) *Response {
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

func handleScreenshot(dev Device, req *Request) *Response {
	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	// Request JPEG directly from the CGO decoder (avoids ~4.6MB image.Decode
	// allocation that pngToJPEG would incur on the fallback PNG path).
	imgData, w, h, mime, err := dev.ScreenshotFormat(avcodec.FormatJPEG)
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("screenshot: %v", err))
	}

	// CLI fallback (ffmpeg) always returns PNG regardless of format request.
	// Convert to JPEG for smaller MCP payload (~10× vs PNG at native res).
	if mime != "image/jpeg" {
		jpgData, jpgErr := avcodec.PNGToJPEG(imgData, 85)
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

func handleGetUIElements(dev Device, req *Request) *Response {
	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	formatType := getFormatFromParams(req)
	maxShow := getMaxElementsFromParams(req, protocol.DefClientMaxElements)
	collectMax := clampCollectMax(maxShow)

	// Handle hierarchical formats via UIFormatter registry
	if f := format.ByName(formatType); f != nil {
		fullElements, err := dev.GetUIFull(collectMax)
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
	elements, err := dev.GetUISummary(collectMax)
	if err != nil {
		elements, err = dev.GetUIElementsFallbackADB(collectMax)
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
	if dev.NativeWidth() > 0 && dev.NativeHeight() > 0 {
		vw, vh = dev.NativeWidth(), dev.NativeHeight()
	}
	legacyFormatted := format.ElementsForLLMWithViewport(elements, maxShow, true, vw, vh)
	return newResultResponse(req.ID, map[string]any{
		"elements":  elements,
		"formatted": legacyFormatted,
		"count":     len(elements),
	})
}

// handleGetClipboard returns the latest clipboard text pushed by the device
// (scrcpy clipboard_autosync) plus an observed flag. observed=false means
// "no change observed since connect" — callers must treat the text as
// unknown, not as an empty clipboard.
func handleGetClipboard(dev Device, req *Request) *Response {
	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}
	text, observed := dev.GetClipboard()
	return newResultResponse(req.ID, map[string]any{
		"clipboard": text,
		"observed":  observed,
	})
}

func handleObserve(dev Device, req *Request) *Response {
	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	formatType := getFormatFromParams(req)
	if formatType == "" {
		formatType = "flatref"
	}
	maxShow := getMaxElementsFromParams(req, protocol.DefClientMaxElements)
	collectMax := clampCollectMax(maxShow)
	isSummary := getSummaryFromParams(req)

	// Hierarchical formats (flatref, jsonl, simplexml, yml): need full UI tree.
	// The concurrent screenshot + GetUIFull capture lives in
	// session.ObserveFull — the actor issues exactly ONE device call per
	// observe, preserving its single-threaded session-ownership model.
	if f := format.ByName(formatType); f != nil {
		res, err := dev.ObserveFull(collectMax, avcodec.FormatJPEG)
		if err != nil {
			if errors.Is(err, session.ErrObserveTimeout) {
				return newErrorResponse(req.ID, ErrTimeout, "observe timed out")
			}
			return newErrorResponse(req.ID, ErrDevice, err.Error())
		}

		// Summary mode: keep only the first N elements (already depth-first
		// from the server, so topmost widgets come first).
		if isSummary && maxShow > 50 {
			maxShow = 50
		}
		if maxShow > 0 && len(res.Elements) > maxShow {
			res.Elements = res.Elements[:maxShow]
		}

		formatted := f.Format(res.Elements)

		// CLI fallback (ffmpeg) always returns PNG regardless of format request.
		// Convert to JPEG for ~10x smaller MCP payload only when needed.
		// Native resolution is preserved; OCR is unaffected because
		// it reads decoded video frames, not this screenshot.
		imgData := res.Image
		if res.Mime != "image/jpeg" {
			jpgData, jpgErr := avcodec.PNGToJPEG(res.Image, 85)
			if jpgErr != nil {
				phonelog.Default().Write("observe: png→jpeg failed, falling back: %v", jpgErr)
				jpgData = res.Image
			}
			imgData = jpgData
		}

		return newStreamedImageResponse(req.ID, map[string]any{
			"text":      formatted,
			"mime_type": "image/jpeg",
			"count":     len(res.Elements),
			"width":     dev.DeviceWidth(),
			"height":    dev.DeviceHeight(),
			"format":    formatType,
		}, "image_data", imgData)
	}

	// Legacy flat format (no format specified or unknown format).
	pngData, elements, err := dev.Observe(collectMax, isSummary)
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("observe: %v", err))
	}

	// Collapse off-screen only in summary mode; full mode keeps all elements.
	// Use NativeW×NativeH — see handleGetUIElements for rationale.
	ovw, ovh := 0, 0
	if isSummary && dev.NativeWidth() > 0 && dev.NativeHeight() > 0 {
		ovw, ovh = dev.NativeWidth(), dev.NativeHeight()
	}
	formatted := format.ElementsForLLMWithViewport(elements, maxShow, isSummary, ovw, ovh)

	return newStreamedImageResponse(req.ID, map[string]any{
		"text":      formatted,
		"mime_type": "image/png",
		"count":     len(elements),
		"width":     dev.DeviceWidth(),
		"height":    dev.DeviceHeight(),
	}, "image_data", pngData)
}

func handleTap(dev Device, req *Request) *Response {
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

	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	sx, sy := dev.ScaleToDevice(x, y)
	if err := dev.Tap(sx, sy); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": fmt.Sprintf("Tapped at (%d, %d)", x, y),
	})
}

func handleTapElement(dev Device, req *Request) *Response {
	params, err := parseParams(req.Params)
	if err != nil {
		return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("invalid params: %v", err))
	}

	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	elements, fastErr := dev.GetUISummary(0) // server default (DefMaxElements)
	if fastErr != nil {
		var fallbackErr error
		elements, fallbackErr = dev.GetUIElementsFallbackADB(0)
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
				sx, sy := dev.ScaleToDevice(el.Center[0], el.Center[1])
				if err := dev.Tap(sx, sy); err != nil {
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
				sx, sy := dev.ScaleToDevice(el.Center[0], el.Center[1])
				if err := dev.Tap(sx, sy); err != nil {
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

func handleSwipe(dev Device, req *Request) *Response {
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

	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	sx1, sy1 := dev.ScaleToDevice(startX, startY)
	sx2, sy2 := dev.ScaleToDevice(endX, endY)
	if err := dev.Swipe(sx1, sy1, sx2, sy2, duration); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": fmt.Sprintf("Swiped from (%d, %d) to (%d, %d)", startX, startY, endX, endY),
	})
}

func handleTypeText(dev Device, req *Request) *Response {
	text := parseStringParam(req.Params, "text")
	if text == "" {
		return newErrorResponse(req.ID, ErrInvalid, "missing required parameter: text")
	}

	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	if err := dev.TypeText(text); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": fmt.Sprintf("Typed: %s", text),
	})
}

func handleBack(dev Device, req *Request) *Response {
	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	if err := dev.Back(); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": "Back pressed",
	})
}

func handleHome(dev Device, req *Request) *Response {
	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	if err := dev.Home(); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": "Home pressed",
	})
}

func handlePressKey(dev Device, req *Request) *Response {
	params, err := parseParams(req.Params)
	if err != nil {
		return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("invalid params: %v", err))
	}

	// Try keycode first, then key name
	if _, ok := params["keycode"]; ok {
		keycode := getInt(params, "keycode")
		if dev == nil {
			return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
		}
		if err := dev.PressKey(keycode); err != nil {
			return newErrorResponse(req.ID, ErrDevice, err.Error())
		}
		return newResultResponse(req.ID, map[string]any{
			"message": fmt.Sprintf("Key %d pressed", keycode),
		})
	}

	if keyName, ok := params["key"].(string); ok {
		// KeycodeFromName normalizes internally (case + whitespace).
		kc := protocol.KeycodeFromName(keyName)
		if kc == 0 {
			return newErrorResponse(req.ID, ErrInvalid, fmt.Sprintf("unknown key name: %q", keyName))
		}
		if dev == nil {
			return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
		}
		if err := dev.PressKey(int(kc)); err != nil {
			return newErrorResponse(req.ID, ErrDevice, err.Error())
		}
		return newResultResponse(req.ID, map[string]any{
			"message": fmt.Sprintf("Key %d pressed", kc),
		})
	}

	return newErrorResponse(req.ID, ErrInvalid, "keycode or key is required")
}

func handleLaunchApp(dev Device, req *Request) *Response {
	appName := parseStringParam(req.Params, "package")
	if appName == "" {
		appName = parseStringParam(req.Params, "app")
	}
	if appName == "" {
		return newErrorResponse(req.ID, ErrInvalid, "app or package is required")
	}

	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}

	if err := dev.LaunchApp(appName); err != nil {
		return newErrorResponse(req.ID, ErrDevice, err.Error())
	}

	return newResultResponse(req.ID, map[string]any{
		"message": fmt.Sprintf("Launched: %s", appName),
	})
}

// handleOCR runs OCR on the current screen, returning recognized text
// regions with their positions (bounding boxes + center points).
//
// This is the fallback path for text that the accessibility tree can't
// see — Compose Canvas-drawn text, bitmap labels, etc.
//
// The OCR service is held by the Dispatcher (not a package global): the
// daemon wires its service via NewDispatcher in New(), while the CLI's
// direct-mode dispatcher has ocr=nil and reports "ocr service not
// initialized" — OCR stays daemon-only by design.
func (d *Dispatcher) handleOCR(dev Device, req *Request) *Response {
	if dev == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}
	if d.ocr == nil {
		return newErrorResponse(req.ID, ErrInternal, "ocr service not initialized")
	}

	// Get screenshot from the device (device owns device I/O). Request JPEG
	// directly from the CGO decoder — it's ~10× smaller than PNG at native
	// resolution and the OCR engine decodes both formats via image.Decode.
	imgData, w, h, _, err := dev.ScreenshotFormat(avcodec.FormatJPEG)
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("screenshot: %v", err))
	}

	// Run OCR via daemon-level service (shared engine, mutex-serialized).
	results, err := d.ocr.Recognize(imgData)
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("ocr: %v", err))
	}

	// Build response with text + positions.
	items := make([]ocrsvc.Result, len(results))
	for i, r := range results {
		cx, cy := r.Center()
		items[i] = ocrsvc.Result{
			Text:       r.Text,
			Box:        r.Box,
			Center:     [2]float64{cx, cy},
			Confidence: r.Confidence,
		}
	}

	return newResultResponse(req.ID, map[string]any{
		"items":        items,
		"count":        len(items),
		"image_width":  w,
		"image_height": h,
	})
}
