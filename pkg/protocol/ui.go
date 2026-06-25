package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// UIDumpRequest is sent to the ui socket to request a UI hierarchy dump.
const UIDumpRequest = "dump\x00"

// UIElement represents a single UI element on screen.
// Compatible with phone-mcp UIElement format.
type UIElement struct {
	Index       int     `json:"index"`
	Text        string  `json:"text"`
	ContentDesc string  `json:"content_desc"`
	ResourceID  string  `json:"resource_id"`
	ClassName   string  `json:"class_name"`
	Bounds      [4]int  `json:"bounds"` // [left, top, right, bottom]
	Center      [2]int  `json:"center"`
	Clickable   bool    `json:"clickable"`
	Enabled     bool    `json:"enabled"`
	Focused     bool    `json:"focused,omitempty"`
	Selected    bool    `json:"selected,omitempty"`
}

// UIDumpResponse is the parsed response from the ui socket.
type UIDumpResponse struct {
	Elements []UIElement `json:"elements"`
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

// WriteUIDumpRequest sends a dump request on the ui socket.
func WriteUIDumpRequest(w io.Writer) error {
	_, err := w.Write([]byte(UIDumpRequest))
	return err
}
