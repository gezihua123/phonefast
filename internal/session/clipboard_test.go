package session

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/gezihua123/phonefast/pkg/protocol"
)

// writeClipboardMsg writes a device→client TYPE_CLIPBOARD message on conn,
// encoded the way scrcpy's DeviceMessageWriter does (v3.3.4):
// type(1) + u32 length + utf8 text.
func writeClipboardMsg(t *testing.T, conn net.Conn, text string) {
	t.Helper()
	buf := []byte{byte(protocol.DeviceMsgClipboard)}
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(text)))
	buf = append(buf, text...)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write clipboard msg: %v", err)
	}
}

// waitFor polls cond until true or the deadline passes (net.Pipe writes are
// synchronous, but the reader goroutine runs concurrently — give it time).
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestReadDeviceMessagesCachesClipboard(t *testing.T) {
	client, server := net.Pipe()
	s := &Session{serial: "test-device", controlConn: client}
	go s.readDeviceMessages()

	writeClipboardMsg(t, server, "First name")
	waitFor(t, func() bool { text, _ := s.GetClipboard(); return text == "First name" }, "first clipboard push")

	// A second push replaces the cached value (latest wins).
	writeClipboardMsg(t, server, "copied text")
	waitFor(t, func() bool { text, _ := s.GetClipboard(); return text == "copied text" }, "second clipboard push")

	server.Close()
}

func TestGetClipboardObservedFlag(t *testing.T) {
	client, server := net.Pipe()
	s := &Session{serial: "test-device", controlConn: client}
	go s.readDeviceMessages()

	// Before any push: unknown, NOT empty.
	if text, observed := s.GetClipboard(); text != "" || observed {
		t.Errorf("fresh session: got (%q, %v), want (\"\", false)", text, observed)
	}

	writeClipboardMsg(t, server, "hello")
	waitFor(t, func() bool {
		_, observed := s.GetClipboard()
		return observed
	}, "observed flag")
	if text, observed := s.GetClipboard(); text != "hello" || !observed {
		t.Errorf("after push: got (%q, %v), want (\"hello\", true)", text, observed)
	}

	server.Close()
}

// TestReadDeviceMessagesMarksControlBrokenOnParseError: a parse failure on a
// LIVE session (not closed by us) must mark the control connection broken so
// IsControlAvailable() flips false and the actor's healthCheck reconnects —
// otherwise the clipboard channel dies silently and stays dead.
func TestReadDeviceMessagesMarksControlBrokenOnParseError(t *testing.T) {
	client, server := net.Pipe()
	s := &Session{serial: "test-device", controlConn: client}
	go s.readDeviceMessages()

	// Unknown device message type → ReadDeviceMessage fails closed.
	if _, err := server.Write([]byte{0x7f}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool { return !s.IsControlAvailable() }, "control marked broken")
	if s.controlErr == nil {
		t.Error("controlErr not set after parse failure")
	}
	server.Close()
}

// TestReadDeviceMessagesSilentOnClose: a session Close() tears the conn down
// from our side — the reader must exit without marking anything broken or
// logging an error (it is the normal teardown path for every reconnect).
func TestReadDeviceMessagesSilentOnClose(t *testing.T) {
	client, server := net.Pipe()
	s := &Session{serial: "test-device", controlConn: client}
	done := make(chan struct{})

	go func() { s.readDeviceMessages(); close(done) }()

	// Close the SESSION (as Close() does: closed=true + conn.Close()), not
	// the raw conn — the reader must distinguish teardown from failure.
	s.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readDeviceMessages did not exit after session close")
	}
	if s.controlErr != nil {
		t.Errorf("controlErr = %v after clean close, want nil", s.controlErr)
	}
	server.Close()
}

func TestGetClipboardEmptyByDefault(t *testing.T) {
	// Zero-value session: no pushes observed → empty text + observed=false.
	s := &Session{}
	if text, observed := s.GetClipboard(); text != "" || observed {
		t.Errorf("got (%q, %v), want (\"\", false)", text, observed)
	}
}
