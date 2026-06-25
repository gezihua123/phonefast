package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestUIDumpRequest(t *testing.T) {
	var buf bytes.Buffer
	err := WriteUIDumpRequest(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(buf.Bytes(), []byte(UIDumpRequest)) {
		t.Errorf("expected %q, got %q", UIDumpRequest, buf.String())
	}
}

func TestReadUIDumpResponse(t *testing.T) {
	resp := UIDumpResponse{
		Elements: []UIElement{
			{
				Index:       0,
				Text:        "Test",
				ContentDesc: "Test desc",
				ResourceID:  "com.test:id/btn",
				ClassName:   "android.widget.Button",
				Bounds:      [4]int{10, 20, 100, 80},
				Center:      [2]int{55, 50},
				Clickable:   true,
				Enabled:     true,
			},
		},
	}

	jsonData, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	// Build wire format: 4-byte length + JSON
	var buf bytes.Buffer
	length := uint32(len(jsonData))
	buf.WriteByte(byte(length >> 24))
	buf.WriteByte(byte(length >> 16))
	buf.WriteByte(byte(length >> 8))
	buf.WriteByte(byte(length))
	buf.Write(jsonData)

	parsed, err := ReadUIDumpResponse(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if len(parsed.Elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(parsed.Elements))
	}

	el := parsed.Elements[0]
	if el.Text != "Test" {
		t.Errorf("expected text 'Test', got %q", el.Text)
	}
	if !el.Clickable {
		t.Error("expected clickable")
	}
}

func TestReadUIDumpResponseInvalidLength(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF}) // huge length

	_, err := ReadUIDumpResponse(&buf)
	if err == nil {
		t.Error("expected error for invalid length")
	}
}
