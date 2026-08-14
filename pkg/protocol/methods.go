package protocol

import (
	"fmt"
	"time"
)

// RPC method names shared by the CLI, MCP tools, and the daemon dispatch
// table. This is the single source of truth for the action vocabulary — the
// string literals were previously duplicated (and drifting) across three
// packages. Callers must use these constants, not string literals.
//
// Note: internal/mcp tool NAMES (mcp.NewTool("...")) must stay equal to the
// corresponding method constant — they are the MCP protocol surface and are
// kept as separate literals there, with a comment linking the two.
const (
	MethodStatus        = "status"
	MethodConnect       = "connect"
	MethodDisconnect    = "disconnect"
	MethodListDevices   = "list_devices"
	MethodScreenshot    = "screenshot"
	MethodGetUIElements = "get_ui_elements"
	MethodObserve       = "observe"
	MethodOCR           = "ocr"
	MethodTap           = "tap"
	MethodTapElement    = "tap_element"
	MethodSwipe         = "swipe"
	MethodTypeText      = "type_text"
	MethodBack          = "back"
	MethodHome          = "home"
	MethodPressKey      = "press_key"
	MethodLaunchApp     = "launch_app"
	MethodWait          = "wait"
)

// DefaultWaitMs is the default duration for "wait" when the caller gives no
// duration_ms. The CLI (waitCmd, runOneAction, runBatch), the daemon's
// handleConn, and the MCP handleWait all use this single constant.
const DefaultWaitMs = 1000

// MaxWaitMs caps any single "wait" duration so a misbehaving caller cannot
// sleep unboundedly. The daemon's handleConn enforces it (protecting its
// per-connection goroutines); local CLI/MCP waits share the same policy via
// SleepWait so the cap lives in exactly one place.
const MaxWaitMs = 60_000

// NormalizeWaitMs applies the shared wait policy: ms <= 0 falls back to
// DefaultWaitMs; a positive maxMs caps the duration. It owns ONLY the
// normalization — callers choose how to sleep (daemon: ctx-interruptible
// select; CLI/MCP: SleepWait).
func NormalizeWaitMs(ms, maxMs int) int {
	if ms <= 0 {
		ms = DefaultWaitMs
	}
	if maxMs > 0 && ms > maxMs {
		ms = maxMs
	}
	return ms
}

// SleepWait performs a local in-process wait: normalize the duration
// (default + MaxWaitMs cap), sleep, and return the shared completion message.
//
// Used by callers that must NOT route wait through the daemon: a daemon-side
// sleep on the device actor's single-threaded event loop would block every
// other request to that device (and the health ticker) for the full duration.
// wait has no device-side effect, so the CLI (waitCmd, runOneAction,
// runBatch) and MCP (handleWait) sleep in-process here.
func SleepWait(ms int) string {
	ms = NormalizeWaitMs(ms, MaxWaitMs)
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return FormatWaitResult(ms)
}

// FormatWaitResult renders the shared "wait" completion message so all five
// wait implementations stay in lockstep ("Waited %dms").
func FormatWaitResult(ms int) string {
	return fmt.Sprintf("Waited %dms", ms)
}
