package daemon

import (
	"github.com/gezihua123/phonefast/internal/session"
	"github.com/gezihua123/phonefast/pkg/avcodec"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// fakeDevice is a minimal in-package Device fake for handler/actor tests.
// It embeds a real *session.Session (zero value, no connections) so every
// Device method is promoted and behaves like today's "dead session" test
// doubles: IsAlive()→false, Close() nil-safe, control ops fail on nil conns.
// Tests override exactly the methods they exercise via the per-method
// override funcs below (nil override = embedded session behavior).
type fakeDevice struct {
	*session.Session
	serial            string
	clipboard         string
	clipboardObserved bool // returned by GetClipboard (overrides embedded session)

	// Optional per-method overrides / error injection.
	tapErr  error
	tapFn   func(x, y int) error
	swipeFn func(x1, y1, x2, y2, durationMs int) error

	screenshotFormatFn func(format avcodec.ImageFormat) ([]byte, int, int, string, error)
	observeFn          func(maxElements int, summary bool) ([]byte, []protocol.UIElement, error)
	observeFullFn      func(maxElements int, format avcodec.ImageFormat) (*session.ObserveFullResult, error)
	getUISummaryFn     func(maxElements int) ([]protocol.UIElement, error)
	getUIFullFn        func(maxElements int) ([]protocol.UIFullElement, error)
	uiFallbackADBFn    func(maxElements int) ([]protocol.UIElement, error)
	pressKeyFn         func(keycode int) error
	launchAppFn        func(packageName string) error
	isAliveFn          func() bool
}

// newFakeDevice returns a dead-device fake with the given serial.
func newFakeDevice(serial string) *fakeDevice {
	return &fakeDevice{Session: &session.Session{}, serial: serial}
}

func (f *fakeDevice) Serial() string { return f.serial }

func (f *fakeDevice) GetClipboard() (string, bool) { return f.clipboard, f.clipboardObserved }

func (f *fakeDevice) Tap(x, y int) error {
	if f.tapFn != nil {
		return f.tapFn(x, y)
	}
	if f.tapErr != nil {
		return f.tapErr
	}
	return nil
}

func (f *fakeDevice) Swipe(x1, y1, x2, y2, durationMs int) error {
	if f.swipeFn != nil {
		return f.swipeFn(x1, y1, x2, y2, durationMs)
	}
	return f.Session.Swipe(x1, y1, x2, y2, durationMs)
}

func (f *fakeDevice) ScreenshotFormat(format avcodec.ImageFormat) ([]byte, int, int, string, error) {
	if f.screenshotFormatFn != nil {
		return f.screenshotFormatFn(format)
	}
	return f.Session.ScreenshotFormat(format)
}

func (f *fakeDevice) Observe(maxElements int, summary bool) ([]byte, []protocol.UIElement, error) {
	if f.observeFn != nil {
		return f.observeFn(maxElements, summary)
	}
	return f.Session.Observe(maxElements, summary)
}

func (f *fakeDevice) ObserveFull(maxElements int, format avcodec.ImageFormat) (*session.ObserveFullResult, error) {
	if f.observeFullFn != nil {
		return f.observeFullFn(maxElements, format)
	}
	return f.Session.ObserveFull(maxElements, format)
}

func (f *fakeDevice) GetUISummary(maxElements int) ([]protocol.UIElement, error) {
	if f.getUISummaryFn != nil {
		return f.getUISummaryFn(maxElements)
	}
	return f.Session.GetUISummary(maxElements)
}

func (f *fakeDevice) GetUIFull(maxElements int) ([]protocol.UIFullElement, error) {
	if f.getUIFullFn != nil {
		return f.getUIFullFn(maxElements)
	}
	return f.Session.GetUIFull(maxElements)
}

func (f *fakeDevice) GetUIElementsFallbackADB(maxElements int) ([]protocol.UIElement, error) {
	if f.uiFallbackADBFn != nil {
		return f.uiFallbackADBFn(maxElements)
	}
	return f.Session.GetUIElementsFallbackADB(maxElements)
}

func (f *fakeDevice) PressKey(keycode int) error {
	if f.pressKeyFn != nil {
		return f.pressKeyFn(keycode)
	}
	return f.Session.PressKey(keycode)
}

func (f *fakeDevice) LaunchApp(packageName string) error {
	if f.launchAppFn != nil {
		return f.launchAppFn(packageName)
	}
	return f.Session.LaunchApp(packageName)
}

func (f *fakeDevice) IsAlive() bool {
	if f.isAliveFn != nil {
		return f.isAliveFn()
	}
	return f.Session.IsAlive()
}

// Compile-time check: fakeDevice satisfies Device.
var _ Device = (*fakeDevice)(nil)
