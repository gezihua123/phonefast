package daemon

import (
	"fmt"

	ocrsvc "github.com/gezihua123/phonefast/internal/ocr"
	"github.com/gezihua123/phonefast/internal/session"
	pkgocr "github.com/gezihua123/phonefast/pkg/ocr"
)

// ocrSvc is the daemon-level OCR singleton, set by Daemon.Run().
// Package-level var follows the same pattern as connectDeviceFn —
// allows Dispatch to access the OCR service without a Daemon receiver.
var ocrSvc *ocrsvc.Service

// SetOCRService wires the daemon-level OCR service into the RPC dispatch.
// Called once by Daemon.Run() at startup.
func SetOCRService(s *ocrsvc.Service) {
	ocrSvc = s
}

// handleOCR runs OCR on the current screen, returning recognized text
// regions with their positions (bounding boxes + center points).
//
// This is the fallback path for text that the accessibility tree can't
// see — Compose Canvas-drawn text, bitmap labels, etc.
func handleOCR(sess *session.Session, req *Request) *Response {
	if sess == nil {
		return newErrorResponse(req.ID, ErrNoDevice, "no device connected")
	}
	if ocrSvc == nil {
		return newErrorResponse(req.ID, ErrInternal, "ocr service not initialized")
	}

	// Get screenshot from session (session owns device I/O).
	pngData, w, h, err := sess.Screenshot()
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("screenshot: %v", err))
	}

	// Run OCR via daemon-level service (shared engine, mutex-serialized).
	results, err := ocrSvc.Recognize(pngData)
	if err != nil {
		return newErrorResponse(req.ID, ErrDevice, fmt.Sprintf("ocr: %v", err))
	}

	// Build response with text + positions.
	items := make([]pkgocr.Result, len(results))
	for i, r := range results {
		cx, cy := r.Center()
		items[i] = pkgocr.Result{
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

