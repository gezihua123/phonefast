// Package protocol defines scrcpy control message encoding/decoding
// and the phonefast UI dump protocol.
package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Control message types (from scrcpy ControlMessage.java).
const (
	TypeInjectKeycode      = 0
	TypeInjectText         = 1
	TypeInjectTouchEvent   = 2
	TypeInjectScrollEvent  = 3
	TypeBackOrScreenOn     = 4
	TypeExpandNotification = 5
	TypeExpandSettings     = 6
	TypeCollapsePanels     = 7
	TypeGetClipboard       = 8
	TypeSetClipboard       = 9
	TypeSetDisplayPower    = 10
	TypeRotateDevice       = 11
	TypeUhidCreate         = 12
	TypeUhidInput          = 13
	TypeUhidDestroy        = 14
	TypeOpenHardKeyboard   = 15
	TypeStartApp           = 16
	TypeResetVideo         = 17
)

// Touch action constants.
const (
	ActionDown          = 0
	ActionUp            = 1
	ActionMove          = 2
	ActionCancel        = 3
	ActionOutside       = 4
	ActionPointerDown   = 5
	ActionPointerUp     = 6
	ActionHoverMove     = 7
	ActionScroll        = 8
	ActionHoverEnter    = 9
	ActionHoverExit     = 10
	ActionButtonPress   = 11
	ActionButtonRelease = 12
)

// Position represents a 2D position (matches scrcpy Position).
type Position struct {
	X, Y             int32
	ScreenW, ScreenH uint16
}

// ControlMessage is the decoded form of a scrcpy control message.
type ControlMessage struct {
	Type int
	// for inject_keycode
	Action    int
	Keycode   int
	Repeat    int
	MetaState int
	// for inject_text
	Text string
	// for inject_touch_event
	PointerID int64
	Position  Position
	Pressure  float32
	ActionBtn int
	Buttons   int
	// for inject_scroll_event
	HScroll float32
	VScroll float32
	// for back_or_screen_on
	ActionBack int
	// for start_app
	AppName string
}

// Encode encodes a ControlMessage to binary format for the control socket.
func (m *ControlMessage) Encode() []byte {
	buf := []byte{byte(m.Type)}

	switch m.Type {
	case TypeInjectKeycode:
		buf = append(buf, byte(m.Action))
		buf = binary.BigEndian.AppendUint32(buf, uint32(m.Keycode))
		buf = binary.BigEndian.AppendUint32(buf, uint32(m.Repeat))
		buf = binary.BigEndian.AppendUint32(buf, uint32(m.MetaState))

	case TypeInjectText:
		textLen := len(m.Text)
		buf = binary.BigEndian.AppendUint32(buf, uint32(textLen))
		buf = append(buf, []byte(m.Text)...)

	case TypeInjectTouchEvent:
		buf = append(buf, byte(m.Action))
		buf = binary.BigEndian.AppendUint64(buf, uint64(m.PointerID))
		buf = appendPos(buf, m.Position)
		// pressure: u16 fixed-point per Binary.u16FixedPointToFloat()
		// decode: uint16 / 65536.0 (0xffff → 1.0 special case)
		// encode: clamp to [0,1], multiply by 65536, cap at 0xffff
		buf = binary.BigEndian.AppendUint16(buf, floatToU16Fixed(m.Pressure))
		buf = binary.BigEndian.AppendUint32(buf, uint32(m.ActionBtn))
		buf = binary.BigEndian.AppendUint32(buf, uint32(m.Buttons))

	case TypeInjectScrollEvent:
		buf = appendPos(buf, m.Position)
		// hScroll/vScroll: i16 fixed-point per Binary.i16FixedPointToFloat() * 16
		// decode: int16 / 32768.0 * 16 → effective range [-16, 16]
		// encode: v in [-16, 16] -> clamp(int16(v * 2048), 0x7fff)
		// scrcpy i16FixedPointToFloat treats 0x7fff as 1.0 (which *16 gives 16.0)
		buf = binary.BigEndian.AppendUint16(buf, uint16(floatToI16Fixed(m.HScroll)))
		buf = binary.BigEndian.AppendUint16(buf, uint16(floatToI16Fixed(m.VScroll)))
		buf = binary.BigEndian.AppendUint32(buf, uint32(m.Buttons))

	case TypeBackOrScreenOn:
		buf = append(buf, byte(m.ActionBack))

	case TypeStartApp:
		app := m.AppName
		if app == "" {
			app = "\x00"
		}
		appBytes := []byte(app)
		// 1-byte unsigned length prefix (scrcpy ControlMessageReader.parseStartApp)
		buf = append(buf, byte(len(appBytes)))
		buf = append(buf, appBytes...)

	case TypeResetVideo:
		// no payload

	case TypeSetDisplayPower:
		on := byte(0)
		if m.ActionBack == 1 {
			on = 1
		}
		buf = append(buf, on)

	default:
		// empty payload for unhandled types
	}

	return buf
}

// Device message types (from scrcpy DeviceMessage.java, v3.3.4). These are
// messages the DEVICE sends to the client over the control socket — a
// separate numbering from the ControlMessage types above (client → device).
// Do NOT confuse DeviceMsgClipboard with TypeGetClipboard (8), which is the
// client→device clipboard REQUEST.
const (
	DeviceMsgClipboard    = 0 // u32 length + utf8 text
	DeviceMsgAckClipboard = 1 // u64 sequence
	DeviceMsgUhidOutput   = 2 // u16 id + u16 length + bytes
)

// deviceMsgMaxClipboard mirrors scrcpy's MESSAGE_MAX_SIZE (1<<18); the text
// payload is capped at MESSAGE_MAX_SIZE-5 by the writer.
const deviceMsgMaxClipboard = 1 << 18

// DeviceMessage is a decoded device → client message from the control socket.
type DeviceMessage struct {
	Type int
	// Text carries the clipboard content for DeviceMsgClipboard pushes.
	Text string
}

// ReadDeviceMessage reads one complete device → client message from the
// control socket. It consumes the full payload of EVERY message type —
// including ones phonefast ignores (ack, uhid) — so the byte stream never
// desyncs: leaving a payload unread would corrupt every following message.
func ReadDeviceMessage(r io.Reader) (*DeviceMessage, error) {
	var typeBuf [1]byte
	if _, err := io.ReadFull(r, typeBuf[:]); err != nil {
		return nil, fmt.Errorf("read device message type: %w", err)
	}

	msg := &DeviceMessage{Type: int(typeBuf[0])}
	switch msg.Type {
	case DeviceMsgClipboard:
		var lenBuf [4]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return nil, fmt.Errorf("read clipboard length: %w", err)
		}
		length := binary.BigEndian.Uint32(lenBuf[:])
		if length > deviceMsgMaxClipboard {
			return nil, fmt.Errorf("invalid clipboard length %d (max %d)", length, deviceMsgMaxClipboard)
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("read clipboard text: %w", err)
		}
		msg.Text = string(data)

	case DeviceMsgAckClipboard:
		var seq [8]byte
		if _, err := io.ReadFull(r, seq[:]); err != nil {
			return nil, fmt.Errorf("read clipboard ack sequence: %w", err)
		}

	case DeviceMsgUhidOutput:
		var head [4]byte // u16 id + u16 length
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return nil, fmt.Errorf("read uhid header: %w", err)
		}
		dataLen := binary.BigEndian.Uint16(head[2:])
		if dataLen > 0 {
			data := make([]byte, dataLen)
			if _, err := io.ReadFull(r, data); err != nil {
				return nil, fmt.Errorf("read uhid data: %w", err)
			}
		}

	default:
		// Unknown type: scrcpy's writer throws on unknown types, so a
		// matching server never sends one. Fail closed rather than guess a
		// payload shape — a wrong guess would desync the stream and poison
		// every following message (silent clipboard corruption). The error
		// also flags a server/version skew the jar-rebuild process should
		// have caught.
		return nil, fmt.Errorf("unknown device message type %d (scrcpy server version skew?)", msg.Type)
	}
	return msg, nil
}

// floatToU16Fixed encodes a float [0,1] as u16 fixed-point matching
// scrcpy's Binary.u16FixedPointToFloat(): decode = uint16 / 65536.0, 0xffff → 1.0
func floatToU16Fixed(p float32) uint16 {
	if p >= 1.0 {
		return 0xffff
	}
	if p <= 0 {
		return 0
	}
	return uint16(p * 65536)
}

// floatToI16Fixed encodes a scroll value in [-16, 16] as i16 fixed-point,
// matching scrcpy's Binary.i16FixedPointToFloat() * 16.
//
//	encode: v * 2048, clamped to int16 range [-32768, 32767]
//	decode (scrcpy): value == 0x7fff ? 1.0 : (value / 32768.0), then * 16
func floatToI16Fixed(v float32) int16 {
	val := float64(v) * 2048
	if val >= 32767 {
		return 32767 // 0x7fff → scrcpy decodes as 1.0 * 16 = 16.0
	}
	if val <= -32768 {
		return -32768 // scrcpy decodes as -1.0 * 16 = -16.0
	}
	return int16(val)
}

func appendPos(buf []byte, pos Position) []byte {
	buf = binary.BigEndian.AppendUint32(buf, uint32(pos.X))
	buf = binary.BigEndian.AppendUint32(buf, uint32(pos.Y))
	buf = binary.BigEndian.AppendUint16(buf, pos.ScreenW)
	buf = binary.BigEndian.AppendUint16(buf, pos.ScreenH)
	return buf
}

// --- Convenience constructors ---

// NewTouchMsg creates a touch event message.
func NewTouchMsg(action int, x, y int32, screenW, screenH uint16) *ControlMessage {
	return &ControlMessage{
		Type:      TypeInjectTouchEvent,
		Action:    action,
		PointerID: -1, // virtual finger (matches scrcpy's Device.DEFAULT_POINTER_ID)
		Position: Position{
			X: x, Y: y,
			ScreenW: screenW, ScreenH: screenH,
		},
		Pressure: 1.0,
	}
}

// NewKeycodeMsg creates a keycode injection message.
func NewKeycodeMsg(action, keycode int) *ControlMessage {
	return &ControlMessage{
		Type:    TypeInjectKeycode,
		Action:  action,
		Keycode: keycode,
	}
}

// NewTextMsg creates a text injection message.
func NewTextMsg(text string) *ControlMessage {
	return &ControlMessage{
		Type: TypeInjectText,
		Text: text,
	}
}

// NewScrollMsg creates a scroll event message.
func NewScrollMsg(x, y int32, screenW, screenH uint16, hScroll, vScroll float32) *ControlMessage {
	return &ControlMessage{
		Type:     TypeInjectScrollEvent,
		Position: Position{X: x, Y: y, ScreenW: screenW, ScreenH: screenH},
		HScroll:  hScroll,
		VScroll:  vScroll,
	}
}

// NewStartAppMsg creates a start-app message.
func NewStartAppMsg(appName string) *ControlMessage {
	return &ControlMessage{
		Type:    TypeStartApp,
		AppName: appName,
	}
}

// Android keycodes.
const (
	KeycodeBack       = 4
	KeycodeHome       = 3
	KeycodeEnter      = 66
	KeycodeDelete     = 67
	KeycodeTab        = 61
	KeycodeSpace      = 62
	KeycodeVolumeUp   = 24
	KeycodeVolumeDown = 25
	KeycodePower      = 26
	KeycodeMenu       = 82
	KeycodeSearch     = 84
	KeycodeEscape     = 111 // hides the soft keyboard on most IMEs

	KeyEventActionDown = 0
	KeyEventActionUp   = 1
)

// KeycodeFromName maps common key names to Android keycodes.
func KeycodeFromName(name string) int {
	// Normalization lives here, not at every call site: key names are
	// case-insensitive and tolerate surrounding whitespace. Numeric strings
	// benefit too (" 4 " resolves to 4).
	name = strings.ToLower(strings.TrimSpace(name))

	keyMap := map[string]int{
		"enter":            KeycodeEnter,
		"tab":              KeycodeTab,
		"delete":           KeycodeDelete,
		"backspace":        KeycodeDelete,
		"space":            KeycodeSpace,
		"volume_up":        KeycodeVolumeUp,
		"volume_down":      KeycodeVolumeDown,
		"power":            KeycodePower,
		"menu":             KeycodeMenu,
		"search":           KeycodeSearch,
		"back":             KeycodeBack,
		"home":             KeycodeHome,
		"escape":           KeycodeEscape,
		"esc":              KeycodeEscape,
		"volume_mute":      164,
		"camera":           27,
		"media_play_pause": 85,
		"media_stop":       86,
		"media_next":       87,
		"media_previous":   88,
		"dpad_up":          19,
		"dpad_down":        20,
		"dpad_left":        21,
		"dpad_right":       22,
		"dpad_center":      23,
		"page_up":          92,
		"page_down":        93,
	}

	if k, ok := keyMap[name]; ok {
		return k
	}

	// Try numeric string
	if v, err := strconv.Atoi(name); err == nil {
		return v
	}

	return 0
}
