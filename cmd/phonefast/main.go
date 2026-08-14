// phonefast — fast Android device control combining scrcpy video streaming
// with phone-mcp compatible MCP tools.
//
// Usage:
//
//	# Default: daemon mode (fast, auto-starts daemon — <10ms per call;
//	#   falls back to direct mode if the daemon can't start)
//	phonefast tap 540 960
//	phonefast back
//	phonefast screenshot /tmp/s.png
//
//	# Direct mode (no daemon, connects each time — ~2.5s per call)
//	phonefast --foreground tap 540 960
//	phonefast --foreground back
//
//	# Daemon management
//	phonefast daemon                # Start daemon in background
//	phonefast daemon --foreground     # Start daemon in foreground (logs to stdout)
//	phonefast daemon --stop           # Stop running daemon
//
//	# Server
//	phonefast serve                  # Start MCP server (SSE on :8019)
//	phonefast serve --transport stdio  # Start MCP server (STDIO)
//	phonefast devices                # List connected devices
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gezihua123/phonefast/internal/adb"
	"github.com/gezihua123/phonefast/internal/daemon"
	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/internal/mcp"
	"github.com/gezihua123/phonefast/internal/session"
	pkgocr "github.com/gezihua123/phonefast/ocr"
	// Register OCR backends into the engine registry via init().
	_ "github.com/gezihua123/phonefast/ocr/apple"
	_ "github.com/gezihua123/phonefast/ocr/onnx"
	_ "github.com/gezihua123/phonefast/ocr/tesseract"
	// _ "github.com/gezihua123/phonefast/ocr/ncnn" // opt-in via -tags ncnn
	"github.com/gezihua123/phonefast/pkg/protocol"
)

const (
	defaultPort = 8019
	defaultPath = "/Phone"
	defaultScid = 0x3f
)

// Version is the phonefast build version. Injected via -ldflags
// "-X main.Version=..." at build time (see scripts/build.sh). Defaults to
// "dev" when built without ldflags (e.g. `go run` / `go build` directly).
var Version = "dev"

// BuildTime and GitCommit are injected via -ldflags at build time.
var BuildTime = "unknown"
var GitCommit = "unknown"

// cmdEnv carries the per-invocation CLI state: routing mode, target serial,
// binary name, and the shared daemon supervisor/dispatcher. It replaced the
// former package-level globals (e.useDaemon/e.serial/e.binName) so command
// handlers no longer read mutable process state — everything flows through
// the explicit receiver.
type cmdEnv struct {
	// e.useDaemon routes commands through the background daemon. Default true —
	// daemon mode for sub-10ms latency. --foreground bypasses the daemon and
	// connects directly (one-shot scrcpy session, ~2.5s).
	useDaemon bool

	// serial is the device serial to bind the session to. Set from --serial
	// flag or auto-detected (first connected device).
	serial string

	// e.binName is the dynamic binary name derived from os.Args[0].
	binName string

	// supervisor owns daemon lifecycle (auto-start/stop/self-heal). Never
	// touched by direct-mode (--foreground) paths — invariant: --foreground
	// must not create a daemon process (guarded by TestForegroundNeverTouchesDaemon).
	supervisor *daemon.Supervisor

	// dispatcher serves the CLI's direct mode (OCR nil — daemon-only).
	dispatcher *daemon.Dispatcher
}

func main() {
	// e carries the whole per-invocation state; command handlers are methods
	// on it. The supervisor is constructed but inert until a daemon-mode path
	// calls EnsureRunning — direct mode (--foreground) never touches it.
	e := &cmdEnv{
		binName:    filepath.Base(os.Args[0]),
		supervisor: daemon.NewSupervisor(daemon.SupervisorConfig{ChildArgs: []string{"daemon_worker"}}),
		// Lazy OCR service: NewService only stores config — the engine and
		// models load on the first Recognize call. Direct-mode OCR therefore
		// costs nothing unless actually used.
		dispatcher: daemon.NewDispatcher(pkgocr.NewService(pkgocr.Config{
			Engine:    os.Getenv("PHONEFAST_OCR_ENGINE"),
			UseVision: os.Getenv("PHONEFAST_OCR_VISION") != "false",
		})),
	}
	if len(os.Args) < 2 {
		e.printUsage()
		os.Exit(1)
	}

	// --version / -v: print build version and exit (before any other parsing).
	if os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Printf("phonefast %s (commit %s, built %s)\n", Version, GitCommit, BuildTime)
		return
	}

	// --help / -h: print usage and exit (before any other parsing).
	if os.Args[1] == "--help" || os.Args[1] == "-h" {
		e.printUsage()
		return
	}

	// Parse mode flags (before the subcommand). Default is daemon mode.
	// --foreground / --direct bypass the daemon; --daemon is kept for backward compat.
	mode, serial, subStart := parseModeFlags(os.Args[1:])
	e.useDaemon = mode
	if serial != "" {
		e.serial = serial
	}
	startIdx := 1 + subStart

	if startIdx >= len(os.Args) {
		e.printUsage()
		os.Exit(1)
	}

	cmd := os.Args[startIdx]
	args := os.Args[startIdx+1:]

	// Reject unknown commands before touching the daemon — a typo shouldn't
	// spawn (or wait up to 8s on) a daemon only to die with "Unknown command".
	if !knownCommand(cmd) {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		e.printUsage()
		os.Exit(1)
	}

	// Auto-start the daemon if needed (before dispatching the command).
	excluded, required := daemonStartMode(cmd)
	if e.useDaemon && !excluded {
		// Resolve serial if not explicitly set
		if e.serial == "" {
			e.serial = resolveSerial()
		}
		if required {
			// connect/disconnect have no direct-mode fallback — a missing
			// daemon is fatal for them, so fail fast instead of degrading to
			// a (nonexistent) direct path.
			e.ensureDaemonOrExit()
		} else {
			e.ensureDaemon()
		}
	} else if !e.useDaemon && e.serial == "" && cmd != "daemon" && cmd != "serve" && cmd != "devices" {
		// Even for non-daemon commands, resolve serial once for consistency
		e.serial = resolveSerial()
	}

	// Mid-RPC self-heal is wired per-Client: daemonCall installs
	// e.supervisor.EnsureRunning as the ensurer on every client it builds.
	// Direct mode (--foreground, or after a fallback) never builds a client,
	// so it never touches the daemon.

	switch cmd {
	// ── Daemon management ──
	case "daemon":
		e.daemonCmd(args)

	// ── CLI commands ──
	case "tap":
		e.tapCmd(args)
	case "tap_element":
		e.tapElementCmd(args)
	case "swipe":
		e.swipeCmd(args)
	case "type", "text":
		e.typeCmd(args)
	case "back":
		e.backCmd()
	case "home":
		e.homeCmd()
	case "key", "press_key":
		e.keyCmd(args)
	case "launch":
		e.launchCmd(args)
	case "screenshot":
		e.screenshotCmd(args)
	case "ui":
		e.uiCmd(args)
	case "observe":
		e.observeCmd(args)
	case "ocr":
		e.ocrCmd(args)
	case "wait":
		e.waitCmd(args)
	case "status":
		e.statusCmd()

	// ── Server / legacy commands ──
	case "serve":
		e.serveCmd(args)
	case "run":
		e.runCmd(args)
	case "devices":
		devicesCmd()
	case "help":
		e.printUsage()
	case "connect":
		e.connectCmd(args)
	case "disconnect":
		e.disconnectCmd(args)

	// Internal daemon child process (not shown in usage)
	case "daemon_worker":
		e.daemonRunCmd(args)

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		e.printUsage()
		os.Exit(1)
	}
}

// ── Serial resolution ──

// parseModeFlags parses the leading mode flags (before the subcommand) from
// the arg list. It returns:
//   - mode: false if --foreground/--direct present (default true)
//   - serial: value following --serial, if present ("" otherwise)
//   - consumed: number of leading args consumed (flags + their values)
//
// Parsing stops at the first non-flag token (the subcommand) or an unknown
// flag. --serial with no value is a fatal error (exits the process), matching
// the previous inline behavior.
func parseModeFlags(argv []string) (mode bool, serial string, consumed int) {
	mode = true // default
	i := 0
	for i < len(argv) && (strings.HasPrefix(argv[i], "--") || argv[i] == "-s") {
		switch argv[i] {
		case "--foreground", "--direct":
			mode = false
			i++
		case "--daemon":
			mode = true
			i++
		case "--serial", "-s":
			if i+1 >= len(argv) {
				fmt.Fprintf(os.Stderr, "Error: %s requires a value\n", argv[i])
				os.Exit(1)
			}
			serial = argv[i+1]
			i += 2
		default:
			// Unknown flag — stop parsing (it belongs to the subcommand).
			return mode, serial, i
		}
	}
	return mode, serial, i
}

// resolveSerial returns the device serial to use, auto-detecting the first
// connected device. Returns "" if none is connected — callers (and the daemon's
// auto-detect path) treat "" as "no device" rather than a literal serial.
func resolveSerial() string {
	devices, err := adb.ListDevices()
	if err != nil || len(devices) == 0 {
		return ""
	}
	return devices[0].Serial
}

// knownCommand reports whether cmd is a dispatchable subcommand. Mirrors the
// dispatch switch in main(); checked before daemon auto-start so a typo fails
// fast instead of spawning a daemon first.
func knownCommand(cmd string) bool {
	switch cmd {
	case "daemon", "daemon_worker", "serve", "run", "devices", "help",
		"connect", "disconnect",
		"tap", "tap_element", "swipe", "type", "text", "back", "home",
		"key", "press_key", "launch", "screenshot", "ui", "observe", "ocr",
		"wait", "status":
		return true
	}
	return false
}

// daemonStartMode decides how a command participates in daemon auto-start:
//   - excluded=true → skip the auto-start block entirely. The command either
//     IS the daemon (daemon/daemon_worker), manages it (serve, which does its
//     own start), is pure ADB (devices), or never touches the daemon
//     (help/status/wait).
//   - required=true → the command has NO direct-mode fallback, so a failed
//     auto-start must fail fast instead of degrading (connect/disconnect are
//     daemon-management RPCs with no withSession equivalent).
//
// Everything else (tap/swipe/screenshot/...) degrades to direct mode on failure.
func daemonStartMode(cmd string) (excluded, required bool) {
	switch cmd {
	case "daemon", "daemon_worker", "serve", "devices", "help", "status", "wait":
		return true, false
	case "connect", "disconnect":
		return false, true
	}
	return false, false
}

// ── Daemon auto-start ──

// ensureDaemon starts the unified daemon if it isn't already running and
// healthy. One-shot CLI commands with a direct-mode fallback use this — on
// failure it degrades to direct mode (fallbackToDirect) instead of killing the
// process, so the command still completes via its withSession branch (~2.5s).
// Commands without a fallback (connect/disconnect) use ensureDaemonOrExit;
// long-lived callers (the MCP server) start the daemon themselves so a failed
// restart surfaces to the tool call rather than flipping process state.
// ensureDaemon starts the unified daemon if it isn't already running and
// healthy (via the in-package Supervisor). One-shot CLI commands with a
// direct-mode fallback use this — on failure it degrades to direct mode
// (fallbackToDirect) instead of killing the process, so the command still
// completes via its direct implementation (~2.5s). Commands without a
// fallback (connect/disconnect) use ensureDaemonOrExit; long-lived callers
// (the MCP server) install EnsureRunning as the per-client ensurer so a
// failed restart surfaces to the individual tool call.
func (e *cmdEnv) ensureDaemon() {
	if err := e.supervisor.EnsureRunning(); err != nil {
		e.fallbackToDirect(err)
	}
}

// ensureDaemonOrExit starts the daemon and exits(1) on failure. Used by
// commands that have no direct-mode fallback (connect/disconnect, and serve
// via its own call) — for them a missing daemon is fatal, not a slower path.
func (e *cmdEnv) ensureDaemonOrExit() {
	if err := e.supervisor.EnsureRunning(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// fallbackToDirect handles a daemon auto-start failure for a one-shot CLI
// command: flip to direct mode so the command dispatches through its direct
// implementation instead of dying. Every daemon-mode command already has a
// direct path, so the fallback is free — the only cost is ~2.5s per-call
// latency.
//
// PHONEFAST_NO_DAEMON_FALLBACK=1 opts out of silent degradation: agents/CI
// that would rather fail than silently run at ~250× the latency (10ms→2.5s
// per call) set this to get a hard error instead of a slow success.
func (e *cmdEnv) fallbackToDirect(err error) {
	if os.Getenv("PHONEFAST_NO_DAEMON_FALLBACK") == "1" {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	fmt.Fprintf(os.Stderr, "Falling back to direct mode (~2.5s for this call). Fix the daemon by running `%s daemon --foreground`\n", e.binName)
	e.useDaemon = false
}

// ── Daemon subcommand ──

func (e *cmdEnv) daemonCmd(args []string) {
	// ── Subcommand dispatch (before flag parsing) ──
	// connect/disconnect are device-level management commands dispatched via RPC
	// to the running daemon. They must come before the "already running" check
	// because they require a live daemon to talk to.
	if len(args) > 0 {
		switch args[0] {
		case "connect":
			e.connectCmd(args[1:])
			return
		case "disconnect":
			e.disconnectCmd(args[1:])
			return
		}
	}

	foreground := false
	doStop := false
	doStatus := false
	ocrVision := true
	// Precedence: --ocr-engine flag > PHONEFAST_OCR_ENGINE env > "onnx".
	// Reading the env as the default lets `PHONEFAST_OCR_ENGINE=ncnn phonefast
	// daemon` work without the flag (otherwise the os.Setenv below would
	// clobber a shell-exported value with "onnx"). The --ocr-engine flag, if
	// given, still wins.
	ocrEngine := os.Getenv("PHONEFAST_OCR_ENGINE")
	if ocrEngine == "" {
		ocrEngine = pkgocr.EngineONNX
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--foreground", "-f":
			foreground = true
		case "--stop":
			doStop = true
		case "--status":
			doStatus = true
		case "--ocr-vision":
			if i+1 < len(args) {
				ocrVision = args[i+1] != "false"
				i++
			}
		case "--ocr-engine":
			if i+1 < len(args) {
				ocrEngine = args[i+1]
				i++
			}
		}
	}

	pidFile := daemon.PidFileName()

	if doStop {
		msg, err := e.supervisor.Stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if msg != "" {
			fmt.Println(msg)
		}
		return
	}

	if doStatus {
		e.statusCmd()
		return
	}

	if pid, _ := daemon.ReadPID(pidFile); pid > 0 && daemon.IsProcessAlive(pid) {
		fmt.Printf("daemon already running (pid %d)\n", pid)
		os.Exit(1)
	}

	// Clean up any stale PID/socket from a prior run.
	if pid, _ := daemon.ReadPID(pidFile); pid > 0 {
		daemon.RemovePID(pidFile)
	}
	os.Remove(daemon.SocketName())

	if foreground {
		if err := os.Setenv("PHONEFAST_OCR_VISION", fmt.Sprintf("%v", ocrVision)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot set PHONEFAST_OCR_VISION: %v\n", err)
		}
		if err := os.Setenv("PHONEFAST_OCR_ENGINE", ocrEngine); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot set PHONEFAST_OCR_ENGINE: %v\n", err)
		}
		runDaemon()
		return
	}

	pid, err := e.supervisor.Spawn([]string{
		fmt.Sprintf("PHONEFAST_OCR_VISION=%v", ocrVision),
		fmt.Sprintf("PHONEFAST_OCR_ENGINE=%s", ocrEngine),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("daemon started (pid %d)\n", pid)
}

// daemonRunCmd handles the hidden internal subcommand daemon_worker.
// This is the child process spawned by "phonefast daemon" — not shown in usage.
func (e *cmdEnv) daemonRunCmd(args []string) {
	runDaemon()
}

func runDaemon() {
	cfg := daemon.Config{
		Foreground: true,
	}
	d := daemon.New(cfg)

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		// When spawned by the Supervisor (or `daemon` in background) stderr goes
		// to /dev/null, so a startup failure would be invisible. Write it to
		// the shared log file — the one the Supervisor's error points at — and
		// flush before exiting.
		phonelog.Default().Write("daemon start failed: %v", err)
		phonelog.CloseDefault()
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}

// ── CLI command dispatcher ──

// daemonCall sends a JSON-RPC request to the device-specific daemon and returns the raw result.
func (e *cmdEnv) daemonCall(method string, params map[string]any) json.RawMessage {
	// WithEnsurer wires mid-RPC self-heal: if the daemon crashes, Client.Call
	// invokes Supervisor.EnsureRunning (restart deduplicated across concurrent
	// callers) and retries once. Only built in daemon mode — direct mode never
	// constructs a client.
	client := daemon.NewClient(e.serial, daemon.WithEnsurer(e.supervisor.EnsureRunning))
	result, err := client.Call(method, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return result
}

// call runs method with identical results in both modes: daemon mode via
// RPC, direct mode via the in-process Dispatcher against a one-shot session.
// consume receives the identical JSON-RPC result either way, so user-visible
// output is byte-for-byte the same in both modes.
func (e *cmdEnv) call(method string, params map[string]any, consume func(json.RawMessage)) {
	if e.useDaemon {
		consume(e.daemonCall(method, params))
		return
	}
	e.withSession(func(dev daemon.Device) error {
		result, err := e.dispatcher.DispatchResult(dev, method, params)
		if err != nil {
			return err
		}
		consume(result)
		return nil
	})
}

// withSession connects to the resolved device, calls fn, and disconnects.
// Used for direct mode (no daemon).
func (e *cmdEnv) withSession(fn func(dev daemon.Device) error) {
	if e.serial == "" {
		fmt.Fprintln(os.Stderr, "Error: no devices connected")
		os.Exit(1)
	}

	serial := e.serial
	sess, err := session.Connect(serial, defaultScid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to device: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	if err := fn(sess); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// ── CLI commands ──

// atoiOrExit parses s as an int (tolerating a single trailing "," or ";") and
// returns it, printing the given usage line and exiting on failure. Non-numeric
// input is reported instead of silently becoming 0 — e.g. "tap abc def" now
// errors rather than tapping at (0,0).
func atoiOrExit(usage, s string) int {
	v, err := strconv.Atoi(strings.TrimRight(s, ",;"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Usage: %s\n", usage)
		fmt.Fprintf(os.Stderr, "  invalid value %q: %v\n", s, err)
		os.Exit(1)
	}
	return v
}

func (e *cmdEnv) tapCmd(args []string) {
	usage := fmt.Sprintf("%s [--foreground] tap <x> <y>", e.binName)
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s\n", usage)
		os.Exit(1)
	}
	x := atoiOrExit(usage, args[0])
	y := atoiOrExit(usage, args[1])

	// Both modes funnel through the same handler (daemon RPC or in-process
	// Dispatcher) — output is identical.
	e.call(protocol.MethodTap, map[string]any{"x": x, "y": y}, printMessage)
}

func (e *cmdEnv) tapElementCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] tap_element <index|text>\n", e.binName)
		fmt.Fprintln(os.Stderr, "  Example: tap_element 5")
		fmt.Fprintln(os.Stderr, "           tap_element \"Settings\"")
		os.Exit(1)
	}

	// Numeric arg → index search; anything else → text search. Both modes
	// share the daemon handler (its search logic is the single source).
	if idx, err := strconv.Atoi(args[0]); err == nil {
		e.call(protocol.MethodTapElement, map[string]any{"index": idx}, printMessage)
	} else {
		e.call(protocol.MethodTapElement, map[string]any{"text": args[0]}, printMessage)
	}
}

func (e *cmdEnv) swipeCmd(args []string) {
	usage := fmt.Sprintf("%s [--foreground] swipe <x1> <y1> <x2> <y2> [duration_ms]", e.binName)
	if len(args) < 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s\n", usage)
		os.Exit(1)
	}
	x1 := atoiOrExit(usage, args[0])
	y1 := atoiOrExit(usage, args[1])
	x2 := atoiOrExit(usage, args[2])
	y2 := atoiOrExit(usage, args[3])
	dur := 500
	if len(args) >= 5 {
		d, err := strconv.Atoi(args[4])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Usage: %s\n", usage)
			fmt.Fprintf(os.Stderr, "  invalid duration %q: %v\n", args[4], err)
			os.Exit(1)
		}
		dur = d
	}

	e.call(protocol.MethodSwipe, map[string]any{
		"start_x": x1, "start_y": y1,
		"end_x": x2, "end_y": y2,
		"duration_ms": dur,
	}, printMessage)
}

func (e *cmdEnv) typeCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] type <text>\n", e.binName)
		os.Exit(1)
	}
	text := strings.Join(args, " ")

	e.call(protocol.MethodTypeText, map[string]any{"text": text}, printMessage)
}

func (e *cmdEnv) backCmd() {
	e.call(protocol.MethodBack, nil, printMessage)
}

func (e *cmdEnv) homeCmd() {
	e.call(protocol.MethodHome, nil, printMessage)
}

func (e *cmdEnv) keyCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] key <keyname|keycode>\n", e.binName)
		fmt.Fprintln(os.Stderr, "  Examples: BACK, HOME, ENTER, TAB, 4, 3")
		os.Exit(1)
	}

	// Numeric → keycode; otherwise resolve the name in the handler (single
	// source of truth: protocol.KeycodeFromName).
	if kc, err := strconv.Atoi(args[0]); err == nil {
		e.call(protocol.MethodPressKey, map[string]any{"keycode": kc}, printMessage)
		return
	}
	e.call(protocol.MethodPressKey, map[string]any{"key": args[0]}, printMessage)
}

func (e *cmdEnv) launchCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] launch <package>\n", e.binName)
		fmt.Fprintln(os.Stderr, "  Example: com.android.settings")
		os.Exit(1)
	}

	e.call(protocol.MethodLaunchApp, map[string]any{"package": args[0]}, printMessage)
}

func (e *cmdEnv) screenshotCmd(args []string) {
	// Single path for both modes: direct mode now returns JPEG exactly like
	// daemon mode (the drift is gone — one handler, one output shape).
	var resp struct {
		Text      string `json:"text"`
		ImageData string `json:"image_data"`
		MimeType  string `json:"mime_type"`
	}
	e.call(protocol.MethodScreenshot, nil, func(result json.RawMessage) {
		if err := json.Unmarshal(result, &resp); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
			os.Exit(1)
		}
		writeScreenshot(args, resp.ImageData, resp.MimeType)
	})
}

func writeScreenshot(args []string, b64, mimeType string) {
	if len(args) == 0 {
		// No path: print as data URI on stdout — the historical no-arg
		// contract scripts/agents parse.
		fmt.Printf("data:%s;base64,%s\n", mimeType, b64)
		return
	}
	imgData, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error decoding screenshot: %v\n", err)
		os.Exit(1)
	}
	outPath := args[0]
	if err := os.WriteFile(outPath, imgData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Screenshot saved to %s\n", outPath)
}

// extFromMimeType maps a MIME type to a file extension, defaulting to png.
func extFromMimeType(mimeType string) string {
	switch mimeType {
	case "image/jpeg", "image/jpg":
		return "jpg"
	default:
		return "png"
	}
}

func (e *cmdEnv) uiCmd(args []string) {
	maxShow, isSummary, formatType := parseUIShowArgs(args, protocol.DefClientMaxElements)

	// Single path: direct mode now honors --format (hierarchical formats) and
	// prints the daemon's formatted output — same in both modes.
	var resp struct {
		Formatted string `json:"formatted"`
	}
	e.call(protocol.MethodGetUIElements, map[string]any{
		"max_elements": maxShow,
		"summary":      isSummary,
		"format":       formatType,
	}, func(result json.RawMessage) {
		json.Unmarshal(result, &resp)
		fmt.Println(resp.Formatted)
	})
}

func (e *cmdEnv) observeCmd(args []string) {
	maxShow, isSummary, formatType := parseUIShowArgs(args, protocol.DefClientMaxElements)
	// formatType is passed through raw: the daemon handler owns the
	// default ("" → "flatref") — no duplicated default in the CLI layer.

	// Single path: hierarchical formats (JPEG + GetUIFull, session-owned
	// concurrency) are identical in both modes.
	var resp struct {
		Text string `json:"text"`
	}
	e.call(protocol.MethodObserve, map[string]any{
		"max_elements": maxShow,
		"summary":      isSummary,
		"format":       formatType,
	}, func(result json.RawMessage) {
		json.Unmarshal(result, &resp)
		fmt.Println(resp.Text)
	})
}

func (e *cmdEnv) ocrCmd(args []string) {
	// Both modes route through the same OCR handler. In direct mode the
	// Dispatcher holds a lazily-created OCR service, so the engine (~90MB)
	// loads only on the first OCR call — other commands pay nothing.
	var resp pkgocr.Response
	e.call(protocol.MethodOCR, map[string]any{}, func(result json.RawMessage) {
		json.Unmarshal(result, &resp)
		if resp.Count == 0 {
			fmt.Println("No text recognized on screen.")
			return
		}
		fmt.Printf("OCR: %d text regions (%dx%d)\n", resp.Count, resp.Width, resp.Height)
		fmt.Println(strings.Repeat("=", 50))
		for i, item := range resp.Items {
			fmt.Printf("[%d] text=%q conf=%.2f center=(%.0f,%.0f)\n",
				i, item.Text, item.Confidence, item.Center[0], item.Center[1])
		}
	})
}

func (e *cmdEnv) waitCmd(args []string) {
	usage := fmt.Sprintf("%s [--foreground] wait <ms>", e.binName)
	var ms int
	if len(args) >= 1 {
		ms = atoiOrExit(usage, args[0])
	}
	// Local sleep via the shared policy helper — never route through the
	// daemon. The daemon-side wait would sleep on the device actor's
	// single-threaded loop, blocking every other request to that device (and
	// the health ticker) for the full duration. wait has no device-side
	// effect, so sleep in-process.
	fmt.Println(protocol.SleepWait(ms))
}

func (e *cmdEnv) statusCmd() {
	// Mode-agnostic: status is a read-only probe of the daemon process
	// (never auto-starting one). Identical output in daemon mode and
	// --foreground mode: the daemon's status as indented JSON.
	pidFile := daemon.PidFileName()
	pid, _ := daemon.ReadPID(pidFile)
	if pid == 0 || !daemon.IsProcessAlive(pid) {
		fmt.Println("daemon not running")
		return
	}
	client := daemon.NewClient(e.serial)
	status, err := client.Ping()
	if err != nil {
		// Daemon process exists but socket not responding — report degraded.
		fmt.Printf("daemon running (pid %d) but not responding: %v\n", pid, err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(data))
}

func (e *cmdEnv) connectCmd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: phonefast connect <serial>")
		fmt.Fprintln(os.Stderr, "       phonefast daemon connect <serial>")
		os.Exit(1)
	}
	serial := args[0]
	client := daemon.NewClient(serial)
	_, err := client.Call(protocol.MethodConnect, map[string]any{"device": serial})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to %s: %v\n", serial, err)
		os.Exit(1)
	}
	fmt.Printf("Connected to %s\n", serial)
}

func (e *cmdEnv) disconnectCmd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: phonefast disconnect <serial>")
		fmt.Fprintln(os.Stderr, "       phonefast daemon disconnect <serial>")
		os.Exit(1)
	}
	serial := args[0]
	client := daemon.NewClient(serial)
	_, err := client.Call(protocol.MethodDisconnect, map[string]any{"device": serial})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error disconnecting %s: %v\n", serial, err)
		os.Exit(1)
	}
	fmt.Printf("Disconnected %s\n", serial)
}

// ── MCP server command (unchanged) ──

func (e *cmdEnv) serveCmd(args []string) {
	cfg := mcp.MCPConfig{
		Transport: "sse",
		Host:      "0.0.0.0",
		Port:      defaultPort,
		Path:      defaultPath,
	}
	// Inherit the global -s/--serial (set before the subcommand, e.g.
	// `phonefast -s DEV serve`); a flag after `serve` overrides it.
	serial := e.serial

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--transport", "-t":
			if i+1 < len(args) {
				cfg.Transport = args[i+1]
				i++
			}
		case "--port", "-p":
			if i+1 < len(args) {
				port, err := strconv.Atoi(args[i+1])
				if err == nil {
					cfg.Port = port
				}
				i++
			}
		case "--host", "-H":
			if i+1 < len(args) {
				cfg.Host = args[i+1]
				i++
			}
		case "--path":
			if i+1 < len(args) {
				cfg.Path = args[i+1]
				i++
			}
		case "--serial", "-s":
			if i+1 < len(args) {
				serial = args[i+1]
				i++
			}
		}
	}

	// Resolve target device (or leave empty for daemon auto-detect).
	if serial == "" {
		serial = resolveSerial()
	}

	// Ensure the unified daemon is running — MCP no longer holds its own
	// session; every tool call routes through the daemon via JSON-RPC.
	// The server has no direct-mode fallback, so a failed auto-start must fail
	// fast here rather than start a server whose every tool call would error.
	e.ensureDaemonOrExit()

	fmt.Fprintf(os.Stderr, "[phonefast] MCP server (device=%s) starting on %s:%d%s/sse\n",
		serial, cfg.Host, cfg.Port, cfg.Path)

	// Wire daemon auto-recovery at the client layer: if the daemon crashes
	// mid-session, daemon.Client.Call detects the unreachable socket, invokes
	// the ensurer (restart deduplicated by the Supervisor), and retries — so
	// a long-lived MCP server self-heals instead of permanently failing every
	// tool call. A failed restart surfaces to the tool call rather than
	// killing the server process.
	server := mcp.New(serial, mcp.WithEnsurer(func() error {
		if err := e.supervisor.EnsureRunning(); err != nil {
			log.Printf("[phonefast] daemon restart failed: %v", err)
			return err
		}
		return nil
	}))
	if err := server.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

// ── run command (single-shot, uses daemon if --daemon flag) ──

// jsonAction represents a single JSON action with optional args.
type jsonAction struct {
	Action string         `json:"action"`
	Args   map[string]any `json:"args"`
}

// normalizeAction ensures Args is populated, collecting flat fields as fallback.
func normalizeAction(rawJSON string, action *jsonAction) {
	if action.Args == nil {
		action.Args = make(map[string]any)
	}
	// If Args is still empty (no "args" key), try reading flat fields from the JSON
	if len(action.Args) == 0 {
		var flat map[string]any
		if err := json.Unmarshal([]byte(rawJSON), &flat); err == nil {
			for k, v := range flat {
				if k != "action" && k != "args" {
					action.Args[k] = v
				}
			}
		}
	}
}

func (e *cmdEnv) runCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Error: run requires a JSON action argument")
		fmt.Fprintf(os.Stderr, "Example: %s run '{\"action\":\"screenshot\"}'\n", e.binName)
		os.Exit(1)
	}

	// Try to parse as JSON array first (batch mode)
	raw := strings.TrimSpace(args[0])
	if strings.HasPrefix(raw, "[") {
		e.runBatch(raw)
		return
	}

	var action jsonAction
	if err := json.Unmarshal([]byte(args[0]), &action); err != nil {
		action.Action = args[0]
	}
	normalizeAction(args[0], &action)

	e.runOneAction(action)
}

// runOneAction executes one action with identical output in both modes.
// Special case for "wait": it sleeps locally instead of routing through the
// daemon — the daemon-side wait would block the device actor's single-threaded
// loop (and the health ticker) for the full duration. wait has no device-side
// effect, so sleep here.
func (e *cmdEnv) runOneAction(action jsonAction) {
	if action.Action == protocol.MethodWait {
		ms, _ := getInt(action.Args, "duration_ms")
		fmt.Println(protocol.SleepWait(ms))
		return
	}
	var resp struct {
		Message string `json:"message"`
	}
	print := func(result json.RawMessage) {
		if err := json.Unmarshal(result, &resp); err == nil && resp.Message != "" {
			fmt.Println(resp.Message)
		} else {
			fmt.Println(string(result))
		}
	}
	e.call(action.Action, action.Args, print)
}

// runBatch executes a JSON array of actions sequentially. Daemon mode sends
// one RPC per action; direct mode opens ONE session and dispatches the whole
// batch in-process (no reconnect per action).
func (e *cmdEnv) runBatch(raw string) {
	var rawItems []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &rawItems); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid JSON array: %v\n", err)
		os.Exit(1)
	}

	if e.useDaemon {
		for _, item := range rawItems {
			var a jsonAction
			json.Unmarshal(item, &a)
			normalizeAction(string(item), &a)
			e.runOneAction(a)
		}
		return
	}

	e.withSession(func(dev daemon.Device) error {
		for _, item := range rawItems {
			var a jsonAction
			json.Unmarshal(item, &a)
			normalizeAction(string(item), &a)
			if a.Action == protocol.MethodWait {
				ms, _ := getInt(a.Args, "duration_ms")
				fmt.Println(protocol.SleepWait(ms))
				continue
			}
			result, err := e.dispatcher.DispatchResult(dev, a.Action, a.Args)
			if err != nil {
				return err
			}
			var resp struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(result, &resp); err == nil && resp.Message != "" {
				fmt.Println(resp.Message)
			} else {
				fmt.Println(string(result))
			}
		}
		return nil
	})
}

func getInt(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

// ── devices command (unchanged) ──

func devicesCmd() {
	devices, err := adb.ListDevices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(devices) == 0 {
		fmt.Println("No devices connected.")
		return
	}

	fmt.Println("Connected devices:")
	for _, d := range devices {
		fmt.Printf("  %s  %s  [%s]\n", d.Serial, d.Status, d.Model)
	}
}

// ── Helpers ──

func parseUIShowArgs(args []string, defaultVal int) (int, bool, string) {
	summary := false
	formatType := ""
	skipNext := false
	filtered := args[:0]
	for _, a := range args {
		if skipNext {
			formatType = a
			skipNext = false
			continue
		}
		if a == "--summary" {
			summary = true
		} else if a == "--format" {
			skipNext = true
		} else {
			filtered = append(filtered, a)
		}
	}
	args = filtered
	if len(args) >= 1 {
		n, err := strconv.Atoi(args[0])
		if err == nil {
			if n < 0 {
				return -1, summary, formatType // show all
			}
			if n > 0 {
				return n, summary, formatType
			}
		}
	}
	return defaultVal, summary, formatType
}

// extractMessage returns the human-readable message for a daemon result: the
// "message" field when present and non-empty, otherwise the raw JSON text.
// Extracted from printMessage so the selection logic is unit-testable without
// capturing stdout.
func extractMessage(result json.RawMessage) string {
	var resp struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(result, &resp); err == nil && resp.Message != "" {
		return resp.Message
	}
	return string(result)
}

func printMessage(result json.RawMessage) {
	fmt.Println(extractMessage(result))
}

func (e *cmdEnv) printUsage() {
	fmt.Print(strings.ReplaceAll(`phonefast — Fast Android device control

Options:
  -s, --serial <serial>          Target a specific device (default: auto-detect)

Commands (default: daemon mode, auto-starts daemon, <10ms):
  phonefast tap <x> <y>                     Tap at coordinates
  phonefast tap_element <idx|txt>           Tap element by index or text
  phonefast swipe <x1> <y1> <x2> <y2> [dur_ms]
  phonefast type <text>                     Type text
  phonefast back                            Press back
  phonefast home                            Press home
  phonefast key <name|keycode>              Send key event
  phonefast launch <package>                Launch app
  phonefast screenshot [file]               Capture screenshot
  phonefast ui [--format <fmt>]             Get UI elements
  phonefast observe [--format <fmt>]        Screenshot + UI
  phonefast wait <ms>                       Wait N ms
    --format: flatref (default) | jsonl | simplexml | yml | flat (legacy)

Direct mode (no daemon, connects each time, ~2.5s):
  phonefast --foreground tap <x> <y>        Tap at coordinates
  phonefast --foreground back               Press back
  ... (prefix with --foreground)

Daemon management:
  phonefast daemon                    Start daemon in background
  phonefast daemon --foreground         Run daemon process in foreground (logs to stdout)
  phonefast daemon --stop               Stop running daemon
  phonefast daemon --status             Check daemon status

Server (MCP):
  phonefast serve                      Start MCP server (SSE mode, :8019)
  phonefast serve --transport stdio    Start MCP server (STDIO mode)
  phonefast serve --port 8080          Start MCP server on custom port

Other:
  phonefast devices                    List connected devices
  phonefast run '<json>'              Single-shot action
  phonefast status                     Show daemon status
  phonefast help                       Show this help message
  phonefast --version                  Show version
  phonefast --help / -h                Show this help message`, "phonefast", e.binName))
}

func init() {
	log.SetFlags(log.Ltime)
}
