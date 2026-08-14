package session

import (
	"fmt"
	"net"
	"os/exec"
	"sync"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/pkg/avcodec"
	"github.com/gezihua123/phonefast/pkg/h264"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// Session represents a live connection to a device via scrcpy sockets.
//
// The display/serial fields are unexported with accessor methods (Serial(),
// DeviceWidth(), ...) so *Session can satisfy the daemon.Device interface —
// Go forbids a method and a field sharing a name.
type Session struct {
	serial  string
	Scid    int
	deviceW int
	deviceH int

	// originalIME holds the IME active before phonefast switched to PFIME,
	// so we can restore it on session close. Empty if PFIME was never activated.
	originalIME string
	// pfime tracks whether PFIME has been activated for this session,
	// avoiding redundant ADB round-trips on subsequent TypeText calls.
	pfime pfimeGate

	// TapDelay controls the DOWN→UP interval used by Tap().
	// Defaults to defaultTapDelay (10ms — a latency floor, not an
	// anti-automation threshold). Set to 0 to use the default.
	// Configurable so callers can tune for different device behavior.
	TapDelay time.Duration

	// nativeW × nativeH is the device's physical display resolution
	// (from "adb shell wm size"). UI elements from both the fast socket
	// and ADB uiautomator dump use this coordinate space.
	// Set once during Connect(); never changes.
	//
	// Touch injection uses deviceW×deviceH (video resolution). Call
	// ScaleToDevice() to convert UI coordinates to touch coordinates.
	nativeW int
	nativeH int

	videoConn   net.Conn
	controlConn net.Conn

	decoder *h264.Decoder

	mu         sync.Mutex
	closed     bool
	controlErr error         // set on first write failure; signals connection is dead
	videoDied  chan struct{} // closed when video drain loop exits (connection dead)
	videoPort  int           // forwarded TCP port for video+control (same socket, multiple accepts)

	// resetMu guards lastResetAt (RESET_VIDEO throttle; see requestKeyframe).
	resetMu     sync.Mutex
	lastResetAt time.Time
	uiPort     int           // forwarded TCP port for UI (fresh connection per request)
	uiReady    bool          // whether UI socket is available

	avDecoder    avcodec.Decoder // CGO go-astiav decoder (lazy init, may be nil)
	avDecoderErr error           // cached init error — don't retry

	uiConn net.Conn // persistent UI socket (reused across GetUI* calls)

	// Test seams (nil in production): when set, ScreenshotFormat / GetUIFull
	// delegate to these instead of touching the device, so wrapper-level
	// policy (ObserveFull's retry-once and error wrapping) is unit-testable
	// without a device. Mirrors the daemon's connectFn injection.
	screenshotFormatFn func(format avcodec.ImageFormat) ([]byte, int, int, string, error)
	getUIFullFn        func(maxElements int) ([]protocol.UIFullElement, error)
}

// defaultTapDelay is the DOWN→UP interval used by Tap() when Session.TapDelay
// is zero. Shared by Connect() and the Tap() fallback so the default lives in
// exactly one place.
const defaultTapDelay = 10 * time.Millisecond

// Connect deploys scrcpy-server, starts it, and establishes all socket connections.
//
// Connection order matters:
//  1. Deploy + start scrcpy-server (tunnel_forward=true → device acts as server)
//  2. Forward video/control socket via ADB
//  3. Connect video socket  → unblocks server's first accept()
//  4. Connect control socket → unblocks server's second accept(); server then runs
//     UISocketHandler.start() which creates the UI abstract socket
//  5. Wait ~500ms for UISocketHandler to bind its socket
//  6. Forward UI socket via ADB
//  7. Probe UI socket availability (no persistent connection — server closes after each dump)
func Connect(serial string, scid int) (*Session, error) {
	s := &Session{
		serial:    serial,
		Scid:      scid,
		TapDelay:  defaultTapDelay,
		videoDied: make(chan struct{}),
	}

	// Ensure PFIME is installed and enabled (best-effort, non-fatal)
	if origIME, err := adb.EnsurePFIME(serial); err == nil {
		s.originalIME = origIME
		phonelog.Default().Write("pfime: original IME = %s", origIME)
	} else {
		phonelog.Default().Write("pfime: ensure failed (non-fatal): %v", err)
	}

	// Step 0: get native display resolution (stable, same space as UI elements).
	// This is required: without it ScaleToDevice() would pass coordinates through
	// unscaled and tap positions would be wrong on devices where NativeW ≠ DeviceW.
	nativeW, nativeH, err := getNativeDisplaySize(serial)
	if err != nil {
		return nil, fmt.Errorf("get native display size: %w", err)
	}
	s.nativeW = nativeW
	s.nativeH = nativeH

	// Step 1: deploy scrcpy-server jar
	info := adb.DefaultScrcpyServer()
	if err := adb.Deploy(serial, info); err != nil {
		return nil, fmt.Errorf("deploy: %w", err)
	}

	// Step 2: kill existing server (StopServer waits for process exit + removes forwards)
	adb.StopServer(serial)

	// Step 3: assign ports (formula owned by pkg/protocol — single source
	// shared with the daemon's ScidAllocator)
	s.videoPort = protocol.ScidPort(scid)
	s.uiPort = s.videoPort + 10

	// Step 4: start server on device (tunnel_forward=true)
	scrcpyArgs := adb.DefaultScrcpyArgs()
	scrcpyArgs.Scid = scid
	if err := adb.StartServer(serial, info, scrcpyArgs); err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}

	// Step 5: forward video/control socket only (UI socket doesn't exist yet)
	socketBase := fmt.Sprintf("scrcpy_%08x", scid)
	if err := adbForward(serial, s.videoPort, socketBase); err != nil {
		s.Close()
		return nil, fmt.Errorf("forward video socket: %w", err)
	}

	// Step 6: connect video + control sockets with full restart on failure.
	// scrcpy tunnel_forward mode: first accept() = video (sends dummy byte),
	// second accept() = control. If either fails the whole session is invalid.
	for attempt := 0; attempt < 3; attempt++ {
		// Connect video socket → unblocks server's first accept()
		s.videoConn, err = dialWithRetry(s.videoPort, 10, 500*time.Millisecond)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("connect video socket: %w", err)
		}
		setKeepAlive(s.videoConn, 30*time.Second)

		// Read the 1-byte dummy sent by the server immediately after first accept()
		s.videoConn.SetDeadline(time.Now().Add(3 * time.Second))
		dummy := make([]byte, 1)
		_, dummyErr := s.videoConn.Read(dummy)
		s.videoConn.SetDeadline(time.Time{})

		if dummyErr == nil {
			break // success — video socket correctly paired
		}

		// Failed — close and restart server from scratch (can't retry individual
		// sockets because scrcpy maps accept order to socket roles).
		phonelog.Default().Write("session: video dummy read failed (attempt %d/3): %v", attempt+1, dummyErr)
		s.videoConn.Close()
		if s.controlConn != nil {
			s.controlConn.Close()
			s.controlConn = nil
		}
		if attempt == 2 {
			s.Close()
			return nil, fmt.Errorf("read dummy byte after %d attempts: %w", attempt+1, dummyErr)
		}

		// Restart server
		adb.StopServer(serial)
		if err := adb.StartServer(serial, info, scrcpyArgs); err != nil {
			s.Close()
			return nil, fmt.Errorf("restart server: %w", err)
		}
		if err := adbForward(serial, s.videoPort, socketBase); err != nil {
			s.Close()
			return nil, fmt.Errorf("re-forward video socket: %w", err)
		}
	}

	// Step 7: connect control socket → unblocks server's second accept()
	// After this, the server continues past DesktopConnection.open() and
	// runs UISocketHandler.start(), creating the UI abstract socket.
	s.controlConn, err = dialWithRetry(s.videoPort, 5, 200*time.Millisecond)
	if err != nil {
		phonelog.Default().Write("warning: control socket unavailable: %v", err)
	} else {
		setKeepAlive(s.controlConn, 15*time.Second)
	}

	// Step 8: read video header (blocks until server sends it)
	s.decoder = h264.NewDecoder()
	if err := s.decoder.ReadVideoHeader(s.videoConn); err != nil {
		s.Close()
		return nil, fmt.Errorf("read video header: %w", err)
	}
	s.deviceW = s.decoder.Width()
	s.deviceH = s.decoder.Height()

	// Step 9: probe UI socket readiness with adaptive polling.
	// UISocketHandler.start() runs after the server reads the video header.
	// Instead of a fixed 600ms sleep, probe at 50ms intervals so a
	// fast-starting socket (typical: ~200ms) returns sooner.
	uiSocketName := socketBase + "_ui"
	if fwdErr := adbForward(serial, s.uiPort, uiSocketName); fwdErr != nil {
		phonelog.Default().Write("warning: ui forward failed: %v", fwdErr)
	} else {
		// Probe loop: up to 20 iterations × 50ms = 1s ceiling
		s.uiReady = probeUISocket(s.uiPort, 20, 50*time.Millisecond)
		if !s.uiReady {
			phonelog.Default().Write("warning: ui socket not ready after probe, using ADB fallback")
		}
	}

	phonelog.Default().Write("connected: %dx%d  control=%t  ui_fast=%t",
		s.deviceW, s.deviceH,
		s.controlConn != nil,
		s.uiReady,
	)

	// Drain frames in background to keep decoder primed
	go s.drainFrames()

	return s, nil
}

func (s *Session) drainFrames() {
	defer close(s.videoDied)
	for {
		// Read conn inside lock to avoid race with Close()
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		conn := s.videoConn
		s.mu.Unlock()

		if conn == nil {
			return
		}

		_, err := s.decoder.ReadFrame(conn)
		if err != nil {
			phonelog.Default().Write("drainFrames: video read error: %v", err)
			return
		}
	}
}

// Close tears down the session.
func (s *Session) Close() error {
	s.mu.Lock()
	s.closed = true
	ctrl := s.controlConn
	s.controlConn = nil
	vid := s.videoConn
	s.mu.Unlock()

	if vid != nil {
		vid.Close()
	}
	if ctrl != nil {
		ctrl.Close()
	}
	if s.uiConn != nil {
		s.uiConn.Close()
		s.uiConn = nil
	}

	adbRemoveForward(s.serial, s.videoPort)
	adbRemoveForward(s.serial, s.uiPort)

	adb.StopServer(s.serial)

	// Restore original IME (best-effort)
	if s.originalIME != "" {
		adb.RestoreIME(s.serial, s.originalIME)
		s.pfime.reset()
	}
	return nil
}

// IsControlAvailable returns whether the control socket is connected and healthy.
func (s *Session) IsControlAvailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controlConn != nil && s.controlErr == nil
}

// IsAlive returns whether the entire session (control + video) is healthy.
func (s *Session) IsAlive() bool {
	if !s.IsControlAvailable() {
		return false
	}
	// Check if video drain goroutine is still running (video connection alive)
	select {
	case <-s.videoDied:
		return false
	default:
		return true
	}
}

// lockControlConn returns the current control connection under the session lock.
// Callers use the returned local copy for I/O so that a concurrent Close() or
// markControlBroken() cannot race between the nil-check and the Write call.
func (s *Session) lockControlConn() net.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controlConn
}

// markControlBroken closes the control connection and records the error.
// All subsequent writes will fail fast, and IsControlAvailable() returns false.
func (s *Session) markControlBroken(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.controlErr == nil {
		s.controlErr = err
	}
	if s.controlConn != nil {
		s.controlConn.Close()
		s.controlConn = nil
	}
}

// IsUIAvailable returns whether the fast UI socket is reachable.
// UI connections are not persistent — each dump opens a fresh connection.
func (s *Session) IsUIAvailable() bool { return s.uiReady }

// --- helpers ---

func adbForward(serial string, localPort int, socketName string) error {
	adbPath, _, err := adb.ADB(serial)
	if err != nil {
		return err
	}

	// Remove stale forward first
	adbRemoveForward(serial, localPort)

	cmd := exec.Command(adbPath, "-s", serial, "forward",
		fmt.Sprintf("tcp:%d", localPort),
		fmt.Sprintf("localabstract:%s", socketName))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("adb forward: %s: %w", string(out), err)
	}

	return nil
}

func adbRemoveForward(serial string, localPort int) {
	if localPort == 0 {
		return
	}
	adbPath, _, err := adb.ADB(serial)
	if err != nil {
		return
	}
	cmd := exec.Command(adbPath, "-s", serial, "forward", "--remove",
		fmt.Sprintf("tcp:%d", localPort))
	cmd.Run()
}

func adbRemoveAllForwards(serial string) {
	adbPath, _, err := adb.ADB(serial)
	if err != nil {
		return
	}
	cmd := exec.Command(adbPath, "-s", serial, "forward", "--remove-all")
	cmd.Run()
}

func dialWithRetry(port int, maxRetries int, interval time.Duration) (net.Conn, error) {
	addr := fmt.Sprintf("localhost:%d", port)
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(interval)
	}
	return nil, fmt.Errorf("dial %s after %d retries: %w", addr, maxRetries, lastErr)
}

// setKeepAlive enables TCP keepalive on the connection with the given interval.
// On macOS/Linux, this sends probes after the idle period and detects dead
// connections within ~1-2 minutes regardless of application traffic.
func setKeepAlive(conn net.Conn, interval time.Duration) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(interval)
	}
}

// probeUISocket repeatedly attempts to connect to the given local TCP port
// until one succeeds or maxAttempts are exhausted.
func probeUISocket(port int, maxAttempts int, interval time.Duration) bool {
	addr := fmt.Sprintf("localhost:%d", port)
	for i := 0; i < maxAttempts; i++ {
		if probe, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			probe.Close()
			return true
		}
		time.Sleep(interval)
	}
	return false
}

// ── Display/serial accessors ──
//
// Exposed as methods (not exported fields) so *Session satisfies the
// daemon.Device interface. Go forbids a method and a field sharing a name,
// hence the unexported fields above.

// Serial returns the device serial this session is connected to.
func (s *Session) Serial() string { return s.serial }

// DeviceWidth returns the video-stream width used for touch injection.
func (s *Session) DeviceWidth() int { return s.deviceW }

// DeviceHeight returns the video-stream height used for touch injection.
func (s *Session) DeviceHeight() int { return s.deviceH }

// NativeWidth returns the physical display width (UI coordinate space).
func (s *Session) NativeWidth() int { return s.nativeW }

// NativeHeight returns the physical display height (UI coordinate space).
func (s *Session) NativeHeight() int { return s.nativeH }

// ScaleToDevice converts UI-element coordinates (in NativeW×NativeH space)
// to device touch coordinates (in DeviceW×DeviceH space).
//
// Touch injection always uses the video resolution (DeviceW×DeviceH) as the
// reference frame — scrcpy internally transforms from video space to physical
// display space. NativeW×NativeH is the fixed display-native coordinate space
// used by both the fast UI socket (AccessibilityNodeInfo.getBoundsInScreen())
// and the ADB uiautomator dump.
func (s *Session) ScaleToDevice(x, y int) (int, int) {
	if s.nativeW == s.deviceW && s.nativeH == s.deviceH {
		return x, y // same resolution, no scaling needed
	}
	sx := int(float64(x) * float64(s.deviceW) / float64(s.nativeW))
	sy := int(float64(y) * float64(s.deviceH) / float64(s.nativeH))
	return sx, sy
}

// getNativeDisplaySize reads the physical display resolution via "adb shell wm size".
// This is the coordinate space used by both AccessibilityNodeInfo.getBoundsInScreen()
// and uiautomator dump.
func getNativeDisplaySize(serial string) (int, int, error) {
	out, err := adb.ADBShell(serial, "wm", "size")
	if err != nil {
		return 0, 0, err
	}
	// Output: "Physical size: 1080x2400" or "Override size: 1080x2400"
	var w, h int
	if _, scanErr := fmt.Sscanf(out, "Physical size: %dx%d", &w, &h); scanErr == nil {
		return w, h, nil
	}
	if _, scanErr := fmt.Sscanf(out, "Override size: %dx%d", &w, &h); scanErr == nil {
		return w, h, nil
	}
	return 0, 0, fmt.Errorf("cannot parse wm size output: %q", out)
}
