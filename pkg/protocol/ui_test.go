package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestUIDumpRequest(t *testing.T) {
	t.Run("default (no limit)", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteUIDumpRequest(&buf, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), []byte("dump\x00")) {
			t.Errorf("expected dump\\0, got %q", buf.String())
		}
	})

	t.Run("with limit", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteUIDumpRequest(&buf, 300)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), []byte("dump:300\x00")) {
			t.Errorf("expected dump:300\\0, got %q", buf.String())
		}
	})

	t.Run("negative is treated as default", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteUIDumpRequest(&buf, -1)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), []byte("dump\x00")) {
			t.Errorf("expected dump\\0 for negative, got %q", buf.String())
		}
	})
}

func TestUISummaryRequest(t *testing.T) {
	t.Run("default summary", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteUISummaryRequest(&buf, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), []byte("sum\x00")) {
			t.Errorf("expected sum\\0, got %q", buf.String())
		}
	})

	t.Run("summary with limit", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteUISummaryRequest(&buf, 80)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), []byte("sum:80\x00")) {
			t.Errorf("expected sum:80\\0, got %q", buf.String())
		}
	})
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
