package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	phonelog "github.com/gezihua123/phonefast/internal/log"
	ocrsvc "github.com/gezihua123/phonefast/ocr"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// ── Dispatch ──

// Dispatcher routes JSON-RPC requests to the device handlers. It owns the
// optional daemon-level OCR service; the CLI's direct mode constructs one
// with ocr=nil (OCR stays daemon-only, preserving today's "OCR requires
// daemon mode" behavior). Being a struct method instead of a free function
// means no package-level globals are needed for handler state.
type Dispatcher struct {
	ocr *ocrsvc.Service
}

// NewDispatcher creates a dispatcher. ocr may be nil when OCR is not
// available (e.g. CLI direct mode).
func NewDispatcher(ocr *ocrsvc.Service) *Dispatcher { return &Dispatcher{ocr: ocr} }

// Dispatch routes a JSON-RPC request to the appropriate handler on the
// given device. dev must be non-nil for all methods except the connectionless
// ones ("status", "list_devices"). Daemon-level methods ("connect",
// "disconnect") are handled in handleConn, not here — they should never reach
// Dispatch in normal operation; the cases here are defensive fallbacks.
func (d *Dispatcher) Dispatch(dev Device, req *Request) *Response {
	phonelog.Default().Write("rpc %s", req.Method)
	switch req.Method {
	case protocol.MethodStatus:
		return handleStatus(dev, req)

	case protocol.MethodConnect:
		return newErrorResponse(req.ID, ErrInternal, "connect is a daemon-level operation; use phonefast daemon connect <serial>")

	case protocol.MethodDisconnect:
		return newErrorResponse(req.ID, ErrInternal, "disconnect is a daemon-level operation; use phonefast daemon disconnect <serial>")

	case protocol.MethodListDevices:
		return handleListDevices(dev, req)

	case protocol.MethodScreenshot:
		return handleScreenshot(dev, req)

	case protocol.MethodGetUIElements:
		return handleGetUIElements(dev, req)

	case protocol.MethodGetClipboard:
		return handleGetClipboard(dev, req)

	case protocol.MethodObserve:
		return handleObserve(dev, req)

	case protocol.MethodOCR:
		return d.handleOCR(dev, req)

	case protocol.MethodTap:
		return handleTap(dev, req)

	case protocol.MethodTapElement:
		return handleTapElement(dev, req)

	case protocol.MethodSwipe:
		return handleSwipe(dev, req)

	case protocol.MethodTypeText:
		return handleTypeText(dev, req)

	case protocol.MethodBack:
		return handleBack(dev, req)

	case protocol.MethodHome:
		return handleHome(dev, req)

	case protocol.MethodPressKey:
		return handlePressKey(dev, req)

	case protocol.MethodLaunchApp:
		return handleLaunchApp(dev, req)

	// "wait" is deliberately NOT routed to the device actor: it has no
	// device-side effect, and a daemon-side time.Sleep would block the
	// actor's single-threaded event loop (stalling every other request +
	// the health ticker for the full duration). Instead, handleConn serves
	// it connectionless (one goroutine per connection, capped at 60s), and
	// CLI/MCP callers sleep locally (waitCmd, runOneAction, mcp handleWait)
	// so it never reaches Dispatch in normal operation.

	default:
		return newErrorResponse(req.ID, ErrMethod, fmt.Sprintf("unknown method: %s", req.Method))
	}
}

// DispatchResult is Dispatch for in-process callers (the CLI's direct mode):
// it materializes the response into the same "result" JSON bytes the wire
// path would emit, or a plain error for error responses. Streaming image
// responses are serialized to their full result object (base64 payload
// included) — identical to what writeStreamedResponse writes over the socket,
// minus the outer JSON-RPC envelope.
func (d *Dispatcher) DispatchResult(dev Device, method string, params map[string]any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	req := &Request{JSONRPC: "2.0", Method: method, Params: raw, ID: 1}
	resp := d.Dispatch(dev, req)
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}
	if resp.streamPayload != nil {
		var buf bytes.Buffer
		writeStreamedResult(&buf, resp)
		return buf.Bytes(), nil
	}
	return resp.Result, nil
}

// writeStreamedResult materializes the result object of a streaming image
// response: {...,"image_data":"<b64>"} — prefix + payload + suffix built by
// newStreamedImageResponse.
func writeStreamedResult(w io.Writer, resp *Response) {
	w.Write(resp.streamPrefix)
	enc := base64.NewEncoder(base64.StdEncoding, w)
	enc.Write(resp.streamPayload)
	enc.Close()
	w.Write(resp.streamSuffix)
}
