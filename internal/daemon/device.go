package daemon

import (
	"github.com/gezihua123/phonefast/internal/session"
	"github.com/gezihua123/phonefast/pkg/avcodec"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// DeviceHealth is the narrow subset of Device used by lifecycle/status code
// (the actor's health loop, reconnect teardown, and status reporting):
// liveness, control/UI availability, close, and identity geometry. Narrow
// consumers take this instead of the full Device so the action/media surface
// doesn't leak into code that never acts.
type DeviceHealth interface {
	IsAlive() bool
	IsControlAvailable() bool
	IsUIAvailable() bool
	Close() error
	Serial() string
	DeviceWidth() int
	DeviceHeight() int
}

// Device is the subset of *session.Session that RPC handlers and the
// DeviceActor use. Handlers depend on this interface, not the concrete
// session type, so daemon business logic is unit-testable without a
// physical device (see fake_device_test.go).
//
// NOTE: the daemon package intentionally stays a single package instead of
// splitting into subpackages (handlers/actor/server) — any subpackage would
// need Request/Response/Device/ScidAllocator/Client/pidfile/Supervisor from
// this package while this package needs the subpackages to serve, an
// immediate import cycle. See docs/DEV.md.
type Device interface {
	DeviceHealth

	// Actions
	Tap(x, y int) error
	Swipe(x1, y1, x2, y2, durationMs int) error
	PressKey(keycode int) error
	Back() error
	Home() error
	TypeText(text string) error
	LaunchApp(packageName string) error
	ScaleToDevice(x, y int) (int, int)

	// Media / UI
	ScreenshotFormat(format avcodec.ImageFormat) ([]byte, int, int, string, error)
	Observe(maxElements int, summary bool) ([]byte, []protocol.UIElement, error)
	ObserveFull(maxElements int, format avcodec.ImageFormat) (*session.ObserveFullResult, error)
	GetUISummary(maxElements int) ([]protocol.UIElement, error)
	GetUIFull(maxElements int) ([]protocol.UIFullElement, error)
	GetUIElementsFallbackADB(maxElements int) ([]protocol.UIElement, error)

	// Geometry
	NativeWidth() int
	NativeHeight() int
}
