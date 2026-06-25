// phonefast — fast Android device control combining scrcpy video streaming
// with phone-mcp compatible MCP tools.
//
// Usage:
//
//	# Default: daemon mode (fast, auto-starts daemon — <10ms per call)
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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	"github.com/gezihua123/phonefast/internal/daemon"
	"github.com/gezihua123/phonefast/internal/mcp"
	"github.com/gezihua123/phonefast/internal/session"
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

// useDaemon controls whether commands are routed through the background daemon.
// Default is true — daemon mode for sub-10ms latency. Use --foreground to
// bypass the daemon and connect directly (one-shot scrcpy session, ~2.5s).
var useDaemon = true

// daemonSerial is the device serial to bind the daemon session to.
// Set from --serial flag or auto-detected (first connected device).
var daemonSerial string

// binName holds the dynamic binary name derived from os.Args[0].
var binName string

func main() {
	binName = filepath.Base(os.Args[0])
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// --version / -v: print build version and exit (before any other parsing).
	if os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Printf("phonefast %s (commit %s, built %s)\n", Version, GitCommit, BuildTime)
		return
	}

	// Parse mode flags (before the subcommand). Default is daemon mode.
	// --foreground / --direct bypass the daemon; --daemon is kept for backward compat.
	mode, serial, subStart := parseModeFlags(os.Args[1:])
	useDaemon = mode
	if serial != "" {
		daemonSerial = serial
	}
	startIdx := 1 + subStart

	if startIdx >= len(os.Args) {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[startIdx]
	args := os.Args[startIdx+1:]

	// Auto-start daemon if needed (before dispatching the command)
	if useDaemon && cmd != "daemon" && cmd != "serve" && cmd != "devices" && cmd != "daemon_worker" {
		// Resolve serial if not explicitly set
		if daemonSerial == "" {
			daemonSerial = resolveSerial()
		}
		ensureDaemon()
	} else if !useDaemon && daemonSerial == "" && cmd != "daemon" && cmd != "serve" && cmd != "devices" {
		// Even for non-daemon commands, resolve serial once for consistency
		daemonSerial = resolveSerial()
	}

	switch cmd {
	// ── Daemon management ──
	case "daemon":
		daemonCmd(args)

	// ── CLI commands ──
	case "tap":
		tapCmd(args)
	case "tap_element":
		tapElementCmd(args)
	case "swipe":
		swipeCmd(args)
	case "type", "text":
		typeCmd(args)
	case "back":
		backCmd()
	case "home":
		homeCmd()
	case "key", "press_key":
		keyCmd(args)
	case "launch":
		launchCmd(args)
	case "screenshot":
		screenshotCmd(args)
	case "ui":
		uiCmd()
	case "observe":
		observeCmd()
	case "wait":
		waitCmd(args)
	case "status":
		statusCmd()

	// ── Server / legacy commands ──
	case "serve":
		serveCmd(args)
	case "run":
		runCmd(args)
	case "devices":
		devicesCmd()
	case "connect":
		connectCmd(args)
	case "disconnect":
		disconnectCmd(args)

	// Internal daemon child process (not shown in usage)
	case "daemon_worker":
		daemonRunCmd(args)

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

// ── Serial resolution ──

// parseModeFlags parses the leading mode flags (before the subcommand) from
// the arg list. It returns:
//   - useDaemon: false if --foreground/--direct present (default true)
//   - serial: value following --serial, if present ("" otherwise)
//   - consumed: number of leading args consumed (flags + their values)
//
// Parsing stops at the first non-flag token (the subcommand) or an unknown
// flag. --serial with no value is a fatal error (exits the process), matching
// the previous inline behavior.
func parseModeFlags(argv []string) (useDaemon bool, serial string, consumed int) {
	useDaemon = true // default
	i := 0
	for i < len(argv) && strings.HasPrefix(argv[i], "--") {
		switch argv[i] {
		case "--foreground", "--direct":
			useDaemon = false
			i++
		case "--daemon":
			useDaemon = true
			i++
		case "--serial":
			if i+1 >= len(argv) {
				fmt.Fprintf(os.Stderr, "Error: --serial requires a value\n")
				os.Exit(1)
			}
			serial = argv[i+1]
			i += 2
		default:
			// Unknown flag — stop parsing (it belongs to the subcommand).
			return useDaemon, serial, i
		}
	}
	return useDaemon, serial, i
}

// resolveSerial returns the device serial to use. Checks --serial flag first,
// then auto-detects the first connected device.
func resolveSerial() string {
	devices, err := adb.ListDevices()
	if err != nil || len(devices) == 0 {
		return "unknown"
	}
	return devices[0].Serial
}

// ── Daemon auto-start ──

func ensureDaemon() {
	pidFile := daemon.PidFileName(daemonSerial)
	socketPath := daemon.SocketName(daemonSerial)

	// Check if daemon is running and actually healthy (responds to ping)
	if pid, _ := daemon.ReadPID(pidFile); pid > 0 && daemon.IsProcessAlive(pid) {
		client := daemon.NewClient(daemonSerial)
		status, err := client.Ping()
		if err == nil {
			if connected, ok := status["connected"].(bool); ok && connected {
				if ctrl, ok := status["control_available"].(bool); ok && ctrl {
					return // daemon is running and healthy
				}
				// Connected but control is dead — daemon will auto-reconnect on next request
				fmt.Fprintf(os.Stderr, "Daemon running but control connection lost, will reconnect...\n")
				return
			}
		}
		// Ping failed or daemon not connected — kill and restart
		fmt.Fprintf(os.Stderr, "Daemon unresponsive, restarting...\n")
		stopDaemonForce(pidFile, pid)
	}

	// Clean up stale files (both new serial-specific and legacy UID-only)
	if pid, _ := daemon.ReadPID(pidFile); pid > 0 {
		daemon.RemovePID(pidFile)
		os.Remove(socketPath)
	}
	os.Remove(daemon.DefaultSocketName())
	daemon.RemovePID(daemon.DefaultPidFileName())

	fmt.Fprintf(os.Stderr, "Starting daemon for device %s...\n", daemonSerial)

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot find executable: %v\n", err)
		os.Exit(1)
	}

	childArgs := []string{"daemon_worker", "--serial", daemonSerial}
	devNull, err := daemonDevNull()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open null device: %v\n", err)
		os.Exit(1)
	}

	child := exec.Command(exe, childArgs...)
	child.Dir = filepath.Dir(exe)
	child.SysProcAttr = daemonSysProcAttr()
	child.Stdin = devNull
	child.Stdout = devNull
	child.Stderr = devNull

	if err := child.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to start daemon: %v\n", err)
		os.Exit(1)
	}
	devNull.Close()

	// Wait for daemon socket to appear and the session to connect.
	// Bounded: ~8s ceiling (was 12s) — long enough for adb forward + device
	// handshake (~2-3s typical), short enough to fail fast on a bad device
	// state instead of blocking every CLI invocation for 12s.
	const waitIter = 40
	readyAt := -1
	for i := 0; i < waitIter; i++ {
		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat(socketPath); err == nil {
			client := daemon.NewClient(daemonSerial)
			status, err := client.Ping()
			if err == nil {
				if connected, ok := status["connected"].(bool); ok && connected {
					return
				}
				// Daemon is up and responding, but the device session isn't
				// connected yet. Fail fast once it's been "up but not
				// connected" for ~3s — a stuck session won't recover by
				// waiting the full loop, and spinning the full 12s blocks
				// every CLI call.
				if readyAt < 0 {
					readyAt = i
				} else if i-readyAt >= 15 {
					fmt.Fprintf(os.Stderr, "Daemon started but device session not connecting; aborting\n")
					stopDaemonForce(pidFile, child.Process.Pid)
					break
				}
			}
		}
		// Child died before opening the socket — no point waiting.
		if !daemon.IsProcessAlive(child.Process.Pid) {
			break
		}
	}

	fmt.Fprintf(os.Stderr, "Error: daemon failed to start (check device connection)\n")
	os.Exit(1)
}

// stopDaemonForce forcefully kills a daemon process and cleans up.
func stopDaemonForce(pidFile string, pid int) {
	daemonKill(pid)
	timeSleep(500)
	if !daemon.IsProcessAlive(pid) {
		daemon.RemovePID(pidFile)
		os.Remove(daemon.SocketName(daemonSerial))
		return
	}
	daemon.RemovePID(pidFile)
	os.Remove(daemon.SocketName(daemonSerial))
}

// ── Daemon subcommand ──

func daemonCmd(args []string) {
	foreground := false
	doStop := false
	doStatus := false
	socketPath := ""
	serial := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--foreground", "-f":
			foreground = true
		case "--stop":
			doStop = true
		case "--status":
			doStatus = true
		case "--socket", "-s":
			if i+1 < len(args) {
				socketPath = args[i+1]
				i++
			}
		case "--serial":
			if i+1 < len(args) {
				serial = args[i+1]
				i++
			}
		}
	}

	// Resolve serial if not provided
	if serial == "" {
		serial = resolveSerial()
	}

	if doStop {
		stopDaemon(serial)
		return
	}

	if doStatus {
		showDaemonStatus(serial)
		return
	}

	pidFile := daemon.PidFileName(serial)
	if pid, _ := daemon.ReadPID(pidFile); pid > 0 && daemon.IsProcessAlive(pid) {
		fmt.Printf("daemon already running (pid %d)\n", pid)
		os.Exit(1)
	}

	if pid, _ := daemon.ReadPID(pidFile); pid > 0 {
		daemon.RemovePID(pidFile)
		if socketPath == "" {
			socketPath = daemon.SocketName(serial)
		}
		os.Remove(socketPath)
	}
	// Also clean up legacy UID-only files
	os.Remove(daemon.DefaultSocketName())
	daemon.RemovePID(daemon.DefaultPidFileName())

	if foreground {
		runDaemon(socketPath, serial)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot find executable: %v\n", err)
		os.Exit(1)
	}

	childArgs := []string{"daemon_worker"}
	if socketPath != "" {
		childArgs = append(childArgs, "--socket", socketPath)
	}
	childArgs = append(childArgs, "--serial", serial)

	devNull, err := daemonDevNull()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open null device: %v\n", err)
		os.Exit(1)
	}

	child := exec.Command(exe, childArgs...)
	child.Dir = filepath.Dir(exe)
	child.SysProcAttr = daemonSysProcAttr()
	child.Stdin = devNull
	child.Stdout = devNull
	child.Stderr = devNull

	if err := child.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to daemonize: %v\n", err)
		os.Exit(1)
	}
	devNull.Close()

	fmt.Printf("daemon started (pid %d)\n", child.Process.Pid)
}

// daemonRunCmd handles the hidden internal subcommand daemon_worker.
// This is the child process spawned by "phonefast daemon" — not shown in usage.
func daemonRunCmd(args []string) {
	socketPath := ""
	serial := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--socket", "-s":
			if i+1 < len(args) {
				socketPath = args[i+1]
				i++
			}
		case "--serial":
			if i+1 < len(args) {
				serial = args[i+1]
				i++
			}
		}
	}

	if serial == "" {
		serial = resolveSerial()
	}
	runDaemon(socketPath, serial)
}

func runDaemon(socketPath, serial string) {
	cfg := daemon.Config{
		Serial:     serial,
		Foreground: true,
		SocketPath: socketPath,
	}
	d := daemon.New(cfg)

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}

func stopDaemon(serial string) {
	pidFile := daemon.PidFileName(serial)
	pid, err := daemon.ReadPID(pidFile)
	if err != nil || pid == 0 {
		fmt.Fprintln(os.Stderr, "daemon not running (no PID file)")
		os.Exit(1)
	}

	if !daemon.IsProcessAlive(pid) {
		fmt.Fprintln(os.Stderr, "daemon not running (stale PID file)")
		daemon.RemovePID(pidFile)
		os.Remove(daemon.SocketName(serial))
		return
	}

	daemonKill(pid)

	for i := 0; i < 50; i++ {
		timeSleep(100)
		if !daemon.IsProcessAlive(pid) {
			fmt.Println("daemon stopped")
			return
		}
	}

	fmt.Fprintln(os.Stderr, "daemon not responding, force killing...")
	daemonKill(pid)
	timeSleep(500)
	daemon.RemovePID(pidFile)
	os.Remove(daemon.SocketName(serial))
	fmt.Println("daemon killed")
}

func showDaemonStatus(serial string) {
	pidFile := daemon.PidFileName(serial)
	pid, _ := daemon.ReadPID(pidFile)

	if pid > 0 && daemon.IsProcessAlive(pid) {
		client := daemon.NewClient(serial)
		status, err := client.Ping()
		if err != nil {
			fmt.Printf("daemon running (pid %d) but not responding: %v\n", pid, err)
			os.Exit(1)
		}
		fmt.Printf("daemon running (pid %d)\n", pid)
		if connected, ok := status["connected"].(bool); ok && connected {
			fmt.Printf("  device:    %v (%vx%v)\n",
				status["serial"], status["device_width"], status["device_height"])
			fmt.Printf("  control:   %v\n", status["control_available"])
			fmt.Printf("  ui:        %v\n", status["ui_available"])
		} else {
			fmt.Println("  device:    not connected")
		}
	} else {
		fmt.Println("daemon not running")
	}
}

// ── CLI command dispatcher ──

// daemonCall sends a JSON-RPC request to the device-specific daemon and returns the raw result.
func daemonCall(method string, params map[string]any) json.RawMessage {
	client := daemon.NewClient(daemonSerial)
	result, err := client.Call(method, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return result
}

// withSession connects to the resolved device, calls fn, and disconnects.
// Used for direct mode (no daemon).
func withSession(fn func(sess *session.Session) error) {
	if daemonSerial == "" || daemonSerial == "unknown" {
		fmt.Fprintln(os.Stderr, "Error: no devices connected")
		os.Exit(1)
	}

	serial := daemonSerial
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

func tapCmd(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] tap <x> <y>\n", binName)
		os.Exit(1)
	}
	x, _ := strconv.Atoi(strings.TrimRight(args[0], ",;"))
	y, _ := strconv.Atoi(strings.TrimRight(args[1], ",;"))

	if useDaemon {
		result := daemonCall("tap", map[string]any{"x": x, "y": y})
		printMessage(result)
	} else {
		withSession(func(sess *session.Session) error {
			sx, sy := sess.ScaleToDevice(x, y)
			return sess.Tap(sx, sy)
		})
		fmt.Printf("Tapped at (%d, %d)\n", x, y)
	}
}

func tapElementCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] tap_element <index|text>\n", binName)
		fmt.Fprintln(os.Stderr, "  Example: tap_element 5")
		fmt.Fprintln(os.Stderr, "           tap_element \"Settings\"")
		os.Exit(1)
	}

	// Determine whether the arg is an index (integer) or text
	idx, err := strconv.Atoi(args[0])
	if err == nil {
		// Numeric index
		if useDaemon {
			result := daemonCall("tap_element", map[string]any{"index": idx})
			printMessage(result)
		} else {
			withSession(func(sess *session.Session) error {
				elements, err := sess.GetUIElements()
				if err != nil {
					elements, err = sess.GetUIElementsFallbackADB()
					if err != nil {
						return fmt.Errorf("get ui elements: %v", err)
					}
				}
				for _, el := range elements {
					if el.Index == idx {
						sx, sy := sess.ScaleToDevice(el.Center[0], el.Center[1])
						if err := sess.Tap(sx, sy); err != nil {
							return err
						}
						fmt.Printf("Tapped element [%d] at (%d, %d)\n", idx, el.Center[0], el.Center[1])
						return nil
					}
				}
				return fmt.Errorf("element with index %d not found", idx)
			})
		}
	} else {
		// Text search
		text := args[0]
		if useDaemon {
			result := daemonCall("tap_element", map[string]any{"text": text})
			printMessage(result)
		} else {
			withSession(func(sess *session.Session) error {
				elements, err := sess.GetUIElements()
				if err != nil {
					elements, err = sess.GetUIElementsFallbackADB()
					if err != nil {
						return fmt.Errorf("get ui elements: %v", err)
					}
				}
				textLower := strings.ToLower(text)
				for _, el := range elements {
					if strings.Contains(strings.ToLower(el.Text), textLower) ||
						strings.Contains(strings.ToLower(el.ContentDesc), textLower) {
						sx, sy := sess.ScaleToDevice(el.Center[0], el.Center[1])
						if err := sess.Tap(sx, sy); err != nil {
							return err
						}
						fmt.Printf("Tapped '%s' at (%d, %d)\n", text, el.Center[0], el.Center[1])
						return nil
					}
				}
				return fmt.Errorf("element with text '%s' not found", text)
			})
		}
	}
}

func swipeCmd(args []string) {
	if len(args) < 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] swipe <x1> <y1> <x2> <y2> [duration_ms]\n", binName)
		os.Exit(1)
	}
	x1, _ := strconv.Atoi(args[0])
	y1, _ := strconv.Atoi(args[1])
	x2, _ := strconv.Atoi(args[2])
	y2, _ := strconv.Atoi(args[3])
	dur := 500
	if len(args) >= 5 {
		dur, _ = strconv.Atoi(args[4])
	}

	if useDaemon {
		result := daemonCall("swipe", map[string]any{
			"start_x": x1, "start_y": y1,
			"end_x": x2, "end_y": y2,
			"duration_ms": dur,
		})
		printMessage(result)
	} else {
		withSession(func(sess *session.Session) error {
			sx1, sy1 := sess.ScaleToDevice(x1, y1)
			sx2, sy2 := sess.ScaleToDevice(x2, y2)
			return sess.Swipe(sx1, sy1, sx2, sy2, dur)
		})
		fmt.Printf("Swiped from (%d, %d) to (%d, %d)\n", x1, y1, x2, y2)
	}
}

func typeCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] type <text>\n", binName)
		os.Exit(1)
	}
	text := strings.Join(args, " ")

	if useDaemon {
		result := daemonCall("type_text", map[string]any{"text": text})
		printMessage(result)
	} else {
		withSession(func(sess *session.Session) error {
			return sess.TypeText(text)
		})
		fmt.Printf("Typed: %s\n", text)
	}
}

func backCmd() {
	if useDaemon {
		result := daemonCall("back", nil)
		printMessage(result)
	} else {
		withSession(func(sess *session.Session) error {
			return sess.Back()
		})
		fmt.Println("Back pressed")
	}
}

func homeCmd() {
	if useDaemon {
		result := daemonCall("home", nil)
		printMessage(result)
	} else {
		withSession(func(sess *session.Session) error {
			return sess.Home()
		})
		fmt.Println("Home pressed")
	}
}

func keyCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] key <keyname|keycode>\n", binName)
		fmt.Fprintln(os.Stderr, "  Examples: BACK, HOME, ENTER, TAB, 4, 3")
		os.Exit(1)
	}

	// Try as numeric keycode first
	if kc, err := strconv.Atoi(args[0]); err == nil {
		if useDaemon {
			result := daemonCall("press_key", map[string]any{"keycode": kc})
			printMessage(result)
		} else {
			withSession(func(sess *session.Session) error {
				return sess.PressKey(kc)
			})
			fmt.Printf("Key %d pressed\n", kc)
		}
		return
	}

	if useDaemon {
		result := daemonCall("press_key", map[string]any{"key": strings.ToLower(args[0])})
		printMessage(result)
	} else {
		kc := keycodeFromName(strings.ToLower(args[0]))
		if kc == 0 {
			fmt.Fprintf(os.Stderr, "Error: unknown key name: %q\n", args[0])
			os.Exit(1)
		}
		withSession(func(sess *session.Session) error {
			return sess.PressKey(int(kc))
		})
		fmt.Printf("Key '%s' pressed\n", args[0])
	}
}

func keycodeFromName(name string) uint32 {
	return uint32(protocol.KeycodeFromName(name))
}

func launchCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--foreground] launch <package>\n", binName)
		fmt.Fprintln(os.Stderr, "  Example: com.android.settings")
		os.Exit(1)
	}

	if useDaemon {
		result := daemonCall("launch_app", map[string]any{"package": args[0]})
		printMessage(result)
	} else {
		withSession(func(sess *session.Session) error {
			return sess.LaunchApp(args[0])
		})
		fmt.Printf("Launched: %s\n", args[0])
	}
}

func screenshotCmd(args []string) {
	if useDaemon {
		result := daemonCall("screenshot", nil)
		var resp struct {
			Text      string `json:"text"`
			ImageData string `json:"image_data"`
			MimeType  string `json:"mime_type"`
		}
		if err := json.Unmarshal(result, &resp); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
			os.Exit(1)
		}
		writeScreenshot(args, resp.ImageData)
	} else {
		withSession(func(sess *session.Session) error {
			pngData, _, _, err := sess.Screenshot()
			if err != nil {
				return err
			}
			writeScreenshot(args, base64.StdEncoding.EncodeToString(pngData))
			return nil
		})
	}
}

func writeScreenshot(args []string, b64 string) {
	pngData, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error decoding screenshot: %v\n", err)
		os.Exit(1)
	}
	if len(args) > 0 {
		outPath := args[0]
		if err := os.WriteFile(outPath, pngData, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Screenshot saved to %s\n", outPath)
	} else {
		// Output as data URI
		fmt.Printf("data:image/png;base64,%s\n", b64)
	}
}

func uiCmd() {
	if useDaemon {
		result := daemonCall("get_ui_elements", nil)
		var resp struct {
			Formatted string `json:"formatted"`
		}
		json.Unmarshal(result, &resp)
		fmt.Println(resp.Formatted)
	} else {
		withSession(func(sess *session.Session) error {
			elements, err := sess.GetUIElements()
			if err != nil {
				elements, err = sess.GetUIElementsFallbackADB()
				if err != nil {
					return err
				}
			}
			// Format elements (mirrored from daemon/rpc.go)
			fmt.Println(formatElements(elements))
			return nil
		})
	}
}

func observeCmd() {
	if useDaemon {
		result := daemonCall("observe", nil)
		var resp struct {
			Text      string `json:"text"`
			ImageData string `json:"image_data"`
			MimeType  string `json:"mime_type"`
		}
		json.Unmarshal(result, &resp)
		fmt.Println(resp.Text)
	} else {
		withSession(func(sess *session.Session) error {
			_, elements, err := sess.Observe()
			if err != nil {
				return err
			}
			fmt.Printf("elements: %d\n", len(elements))
			fmt.Println(formatElements(elements))
			return nil
		})
	}
}

func waitCmd(args []string) {
	ms := 1000
	if len(args) >= 1 {
		ms, _ = strconv.Atoi(args[0])
	}

	if useDaemon {
		result := daemonCall("wait", map[string]any{"duration_ms": ms})
		printMessage(result)
	} else {
		time.Sleep(time.Duration(ms) * time.Millisecond)
		fmt.Printf("Waited %dms\n", ms)
	}
}

func statusCmd() {
	if daemonSerial == "" {
		daemonSerial = resolveSerial()
	}
	if !useDaemon {
		showDaemonStatus(daemonSerial)
		return
	}
	client := daemon.NewClient(daemonSerial)
	status, err := client.Ping()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(data))
}

func connectCmd(args []string) {
	fmt.Fprintf(os.Stderr, "Use '%s daemon --stop' then '%s daemon [--serial SERIAL]' to reconnect\n", binName, binName)
	os.Exit(1)
}

func disconnectCmd(args []string) {
	fmt.Fprintf(os.Stderr, "Use '%s daemon --stop' to disconnect and stop the daemon\n", binName)
	os.Exit(1)
}

// ── MCP server command (unchanged) ──

func serveCmd(args []string) {
	cfg := mcp.MCPConfig{
		Transport: "sse",
		Host:      "0.0.0.0",
		Port:      defaultPort,
		Path:      defaultPath,
	}

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
		}
	}

	devices, err := adb.ListDevices()
	if err != nil || len(devices) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no devices connected")
		os.Exit(1)
	}

	if cfg.Transport == "stdio" {
		server := mcp.New(nil, "", defaultScid)

		// Lazy-connect in the background. Retry on failure instead of giving
		// up: otherwise a transient connect error (device busy, scrcpy
		// already running, ADB hiccup) leaves the MCP server permanently
		// replying "device connecting, retry" with no path to recovery.
		go func() {
			serial := devices[0].Serial
			backoff := 2 * time.Second
			for {
				log.Printf("[phonefast] connecting to device %s...", serial)
				sess, err := session.Connect(serial, defaultScid)
				if err != nil {
					log.Printf("[phonefast] device connection failed: %v — retrying in %v", err, backoff)
					time.Sleep(backoff)
					if backoff < 30*time.Second {
						backoff *= 2
					}
					continue
				}
				server.SetSession(sess, serial)
				log.Printf("[phonefast] device ready: %s", serial)
				return
			}
		}()

		if err := server.Run(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	serial := devices[0].Serial
	fmt.Fprintf(os.Stderr, "[phonefast] connecting to device %s...\n", serial)

	sess, err := session.Connect(serial, defaultScid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to device: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	fmt.Fprintf(os.Stderr, "[phonefast] MCP server starting on %s:%d%s/sse\n",
		cfg.Host, cfg.Port, cfg.Path)

	server := mcp.New(sess, serial, defaultScid)
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

func runCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Error: run requires a JSON action argument")
		fmt.Fprintf(os.Stderr, "Example: %s run '{\"action\":\"screenshot\"}'\n", binName)
		os.Exit(1)
	}

	// Try to parse as JSON array first (batch mode)
	raw := strings.TrimSpace(args[0])
	if strings.HasPrefix(raw, "[") {
		runBatch(raw)
		return
	}

	var action jsonAction
	if err := json.Unmarshal([]byte(args[0]), &action); err != nil {
		action.Action = args[0]
	}
	normalizeAction(args[0], &action)

	if useDaemon {
		result := daemonCall(action.Action, action.Args)
		var resp struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(result, &resp); err == nil && resp.Message != "" {
			fmt.Println(resp.Message)
		} else {
			fmt.Println(string(result))
		}
	} else {
		// Direct mode — dispatch via session
		withSession(func(sess *session.Session) error {
			return dispatchDirect(sess, action)
		})
	}
}

// runBatch executes a JSON array of actions sequentially.
func runBatch(raw string) {
	var rawItems []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &rawItems); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid JSON array: %v\n", err)
		os.Exit(1)
	}

	if useDaemon {
		for _, item := range rawItems {
			var a jsonAction
			json.Unmarshal(item, &a)
			normalizeAction(string(item), &a)
			result := daemonCall(a.Action, a.Args)
			var resp struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(result, &resp); err == nil && resp.Message != "" {
				fmt.Println(resp.Message)
			} else {
				fmt.Println(string(result))
			}
		}
	} else {
		withSession(func(sess *session.Session) error {
			for _, item := range rawItems {
				var a jsonAction
				json.Unmarshal(item, &a)
				normalizeAction(string(item), &a)
				if err := dispatchDirect(sess, a); err != nil {
					return err
				}
			}
			return nil
		})
	}
}

// dispatchDirect runs one action against an open session.
func dispatchDirect(sess *session.Session, action jsonAction) error {
	switch action.Action {
	case "screenshot":
		png, w, h, err := sess.Screenshot()
		if err != nil {
			return err
		}
		b64 := base64.StdEncoding.EncodeToString(png)
		fmt.Printf(`{"base64":"%s","width":%d,"height":%d,"format":"png"}`+"\n", b64, w, h)
	case "get_ui_elements":
		elements, err := sess.GetUIElements()
		if err != nil {
			return err
		}
		data, _ := json.Marshal(elements)
		fmt.Printf(`{"elements":%s}`+"\n", string(data))
	case "observe":
		png, w, h, err := sess.Screenshot()
		if err != nil {
			return err
		}
		elements, err := sess.GetUIElements()
		if err != nil {
			return err
		}
		b64 := base64.StdEncoding.EncodeToString(png)
		fmt.Printf(`{"screenshot_base64":"%s","width":%d,"height":%d,"format":"png","element_count":%d}`+"\n", b64, w, h, len(elements))
	case "tap":
		x, _ := getInt(action.Args, "x")
		y, _ := getInt(action.Args, "y")
		if err := sess.Tap(x, y); err != nil {
			return err
		}
		fmt.Printf("Tapped at (%d, %d)\n", x, y)
	case "tap_element":
		elements, err := sess.GetUIElements()
		if err != nil {
			elements, err = sess.GetUIElementsFallbackADB()
			if err != nil {
				return fmt.Errorf("get ui elements: %v", err)
			}
		}
		if len(elements) == 0 {
			return fmt.Errorf("no UI elements found")
		}
		// Search by index
		if idx, ok := getInt(action.Args, "index"); ok {
			for _, el := range elements {
				if el.Index == idx {
					if err := sess.Tap(el.Center[0], el.Center[1]); err != nil {
						return err
					}
					fmt.Printf("Tapped element [%d] at (%d, %d)\n", idx, el.Center[0], el.Center[1])
					return nil
				}
			}
			return fmt.Errorf("element with index %d not found", idx)
		}
		// Search by text
		if text, ok := action.Args["text"].(string); ok && text != "" {
			textLower := strings.ToLower(text)
			for _, el := range elements {
				if strings.Contains(strings.ToLower(el.Text), textLower) ||
					strings.Contains(strings.ToLower(el.ContentDesc), textLower) {
					if err := sess.Tap(el.Center[0], el.Center[1]); err != nil {
						return err
					}
					fmt.Printf("Tapped '%s' at (%d, %d)\n", text, el.Center[0], el.Center[1])
					return nil
				}
			}
			return fmt.Errorf("element with text '%s' not found", text)
		}
		return fmt.Errorf("specify index=N or text=\"...\"")
	case "back":
		if err := sess.Back(); err != nil {
			return err
		}
		fmt.Println("Back pressed")
	case "home":
		if err := sess.Home(); err != nil {
			return err
		}
		fmt.Println("Home pressed")
	case "type_text":
		text, _ := action.Args["text"].(string)
		if err := sess.TypeText(text); err != nil {
			return err
		}
		fmt.Printf("Typed: %s\n", text)
	case "swipe":
		x1, _ := getInt(action.Args, "start_x")
		y1, _ := getInt(action.Args, "start_y")
		x2, _ := getInt(action.Args, "end_x")
		y2, _ := getInt(action.Args, "end_y")
		dur, _ := getInt(action.Args, "duration_ms")
		if dur == 0 {
			dur = 500
		}
		if err := sess.Swipe(x1, y1, x2, y2, dur); err != nil {
			return err
		}
		fmt.Printf("Swiped from (%d, %d) to (%d, %d)\n", x1, y1, x2, y2)
	case "launch_app":
		pkg, _ := action.Args["package"].(string)
		if pkg == "" {
			pkg, _ = action.Args["app"].(string)
		}
		if err := sess.LaunchApp(pkg); err != nil {
			return err
		}
		fmt.Printf("Launched: %s\n", pkg)
	case "list_devices":
		devices, err := adb.ListDevices()
		if err != nil {
			return err
		}
		data, _ := json.Marshal(devices)
		fmt.Println(string(data))
	case "wait":
		ms, _ := getInt(action.Args, "duration_ms")
		if ms == 0 {
			ms = 1000
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		fmt.Printf("Waited %dms\n", ms)
	case "press_key":
		// Try keycode first, then key name
		if kc, ok := getInt(action.Args, "keycode"); ok {
			if err := sess.PressKey(kc); err != nil {
				return err
			}
			fmt.Printf("Key %d pressed\n", kc)
		} else if keyName, ok := action.Args["key"].(string); ok {
			kc := int(keycodeFromName(strings.ToLower(strings.TrimSpace(keyName))))
			if kc == 0 {
				return fmt.Errorf("unknown key name: %q", keyName)
			}
			if err := sess.PressKey(kc); err != nil {
				return err
			}
			fmt.Printf("Key '%s' pressed\n", keyName)
		} else {
			return fmt.Errorf("press_key requires keycode or key parameter")
		}
	default:
		return fmt.Errorf("unknown action: %s", action.Action)
	}
	return nil
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

// ── Element formatting (for direct mode ui/observe) ──

func formatElements(elements []protocol.UIElement) string {
	if len(elements) == 0 {
		return "No interactive elements found on screen."
	}
	maxShow := 50
	var lines []string
	lines = append(lines, "Interactive elements on screen:")
	lines = append(lines, strings.Repeat("=", 50))
	for i, el := range elements {
		if i >= maxShow {
			lines = append(lines, fmt.Sprintf("... and %d more elements", len(elements)-maxShow))
			break
		}
		line := fmt.Sprintf("[%d]", el.Index)
		if el.Text != "" {
			line += fmt.Sprintf(` text="%s"`, el.Text)
		}
		if el.ContentDesc != "" {
			line += fmt.Sprintf(` desc="%s"`, el.ContentDesc)
		}
		if el.ResourceID != "" {
			id := el.ResourceID
			if idx := strings.LastIndex(id, "/"); idx >= 0 {
				id = id[idx+1:]
			}
			line += fmt.Sprintf(` id="%s"`, id)
		}
		if el.ClassName != "" {
			cn := el.ClassName
			if idx := strings.LastIndex(cn, "."); idx >= 0 {
				cn = cn[idx+1:]
			}
			line += fmt.Sprintf(" (%s)", cn)
		}
		if el.Clickable {
			line += " [clickable]"
		}
		line += fmt.Sprintf(" bounds=[%d,%d][%d,%d]",
			el.Bounds[0], el.Bounds[1], el.Bounds[2], el.Bounds[3])
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// ── Helpers ──

func printMessage(result json.RawMessage) {
	var resp struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(result, &resp); err == nil && resp.Message != "" {
		fmt.Println(resp.Message)
	} else {
		fmt.Println(string(result))
	}
}

func timeSleep(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

func printUsage() {
	fmt.Print(strings.ReplaceAll(`phonefast — Fast Android device control

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
  phonefast ui                              Get UI elements
  phonefast observe                         Screenshot + UI
  phonefast wait <ms>                       Wait N ms

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
  phonefast --version                  Show version`, "phonefast", binName))
}

func init() {
	log.SetFlags(log.Ltime)
}
