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

func TestSimplifyClassName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"android.widget.TextView", "Text"},
		{"android.widget.ImageView", "Image"},
		{"android.widget.Button", "Button"},
		{"android.widget.EditText", "Input"},
		{"android.widget.CheckBox", "Check"},
		{"android.widget.Switch", "Switch"},
		{"android.widget.ProgressBar", "Progress"},
		{"android.widget.SeekBar", "Seek"},
		{"android.widget.RatingBar", "Rating"},
		{"android.widget.Spinner", "Select"},
		{"android.widget.ToggleButton", "Toggle"},
		{"android.widget.ImageButton", "IconBtn"},
		{"android.webkit.WebView", "Browser"},
		{"android.widget.FrameLayout", "FrameLayout"},   // not in widget map — unchanged
		{"android.widget.LinearLayout", "LinearLayout"}, // not in widget map — unchanged
		{"com.example.CustomView", "CustomView"},         // unknown — unchanged
		{"TextView", "Text"},                             // already simple
		{"ImageView", "Image"},                           // already simple
		{"", ""},                                         // empty
	}

	for _, tt := range tests {
		got := SimplifyClassName(tt.input)
		if got != tt.expected {
			t.Errorf("SimplifyClassName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSimplifyClassNameAppCompatVariants(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"androidx.appcompat.widget.AppCompatTextView", "Text"},
		{"androidx.appcompat.widget.AppCompatImageView", "Image"},
		{"androidx.appcompat.widget.AppCompatButton", "Button"},
		{"androidx.appcompat.widget.AppCompatEditText", "Input"},
		{"com.google.android.material.button.MaterialButton", "Button"},
		{"com.google.android.material.textview.MaterialTextView", "Text"},
		{"androidx.appcompat.widget.SwitchCompat", "Switch"},
	}

	for _, tt := range tests {
		got := SimplifyClassName(tt.input)
		if got != tt.expected {
			t.Errorf("SimplifyClassName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsLayoutClass(t *testing.T) {
	tests := []struct {
		input    string
		isLayout bool
	}{
		{"android.widget.FrameLayout", true},
		{"android.widget.LinearLayout", true},
		{"android.widget.RelativeLayout", true},
		{"androidx.constraintlayout.widget.ConstraintLayout", true},
		{"android.widget.ScrollView", true},
		{"androidx.coordinatorlayout.widget.CoordinatorLayout", true},
		{"com.google.android.material.bottomnavigation.BottomNavigationView", true},
		{"androidx.viewpager.widget.ViewPager", true},
		{"androidx.viewpager2.widget.ViewPager2", true},
		{"android.widget.Toolbar", true},
		{"androidx.appcompat.widget.Toolbar", true},
		{"com.google.android.material.tabs.TabLayout", true},
		{"android.widget.TextView", false},
		{"android.widget.ImageView", false},
		{"android.widget.Button", false},
		{"android.widget.EditText", false},
		{"com.example.MyCustomClass", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsLayoutClass(tt.input)
		if got != tt.isLayout {
			t.Errorf("IsLayoutClass(%q) = %v, want %v", tt.input, got, tt.isLayout)
		}
	}
}
