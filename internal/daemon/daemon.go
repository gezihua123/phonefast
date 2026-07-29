package daemon

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof" // registers handlers on DefaultServeMux
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	"github.com/gezihua123/phonefast/internal/ocr"
	phonelog "github.com/gezihua123/phonefast/internal/log"
)

// Daemon is a long-running process that holds device sessions and serves
// JSON-RPC requests over a Unix domain socket.
//
// Each device is managed by a DeviceActor goroutine that exclusively owns
// its session — no mutex is needed for session access. Communication between
// the accept loop and device actors goes through channels.
type Daemon struct {
	devices   map[string]*DeviceActor // serial → device actor
	mu        sync.RWMutex            // protects map access only
	scidAlloc *ScidAllocator          // assigns collision-free scids to actors

	// Per-serial connect serialization. Two concurrent first-requests for the
	// SAME device would each run newDeviceActor → session.Connect, and Connect
	// kills the device's existing scrcpy server (pkill -f scrcpy.Server, by
	// serial — not by scid). So a loser's Connect would tear down the winner's
	// freshly-started server. The per-serial mutex makes same-device connects
	// serial while different devices still connect in parallel.
	// connectMuMu guards the connectMu map itself.
	connectMu   map[string]*sync.Mutex
	connectMuMu sync.Mutex

	ocrService *ocr.Service // daemon-level OCR singleton (lazy init)

	listener   net.Listener
	pidFile    string
	socketPath string
	startedAt  string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	lockFile    *os.File      // PID file handle held for the process lifetime (flock)
	pprofServer *http.Server  // non-nil when pprof is enabled (PHONEFAST_PPROF)
}

// Config holds daemon startup settings.
type Config struct {
	Foreground bool // stay in foreground (don't daemonize)
}

// StatusInfo holds runtime daemon status.
type StatusInfo struct {
	Connected    bool   `json:"connected"`
	Serial       string `json:"serial,omitempty"`
	DeviceWidth  int    `json:"device_width,omitempty"`
	DeviceHeight int    `json:"device_height,omitempty"`
	ControlAvail bool   `json:"control_available"`
	UIAvail      bool   `json:"ui_available"`
	SocketPath   string `json:"socket_path"`
	Pid          int    `json:"pid"`
	StartedAt    string `json:"started_at,omitempty"`
}

// New creates a new Daemon (does NOT connect to device yet).
func New(cfg Config) *Daemon {
	return &Daemon{
		devices:    make(map[string]*DeviceActor),
		scidAlloc:  NewScidAllocator(),
		connectMu:  make(map[string]*sync.Mutex),
		ocrService: ocr.NewService(ocr.Config{
			Engine:    os.Getenv("PHONEFAST_OCR_ENGINE"),          // "onnx" (default) | "ncnn"
			UseVision: os.Getenv("PHONEFAST_OCR_VISION") != "false",
		}),
		socketPath: SocketName(),
		pidFile:    PidFileName(),
	}
}

// Status returns daemon-level info plus status for the first connected device
// (if any), for backward compatibility with single-device callers.
func (d *Daemon) Status() StatusInfo {
	s := StatusInfo{
		SocketPath: d.socketPath,
		Pid:        os.Getpid(),
		StartedAt:  d.startedAt,
	}
	snap := d.snapshotDaemon()
	if len(snap.connected) > 0 {
		as := snap.connected[0]
		s.Connected = as.Connected
		s.Serial = as.Serial
		s.DeviceWidth = as.DeviceWidth
		s.DeviceHeight = as.DeviceHeight
		s.ControlAvail = as.ControlAvail
		s.UIAvail = as.UIAvail
	}
	return s
}

// connectedSnapshot is one connected device's status, captured under the
// daemon RLock for status reporting.
type connectedSnapshot struct {
	Connected    bool   `json:"connected"`
	Serial       string `json:"serial"`
	DeviceWidth  int    `json:"width,omitempty"`
	DeviceHeight int    `json:"height,omitempty"`
	ControlAvail bool   `json:"control_available,omitempty"`
	UIAvail      bool   `json:"ui_available,omitempty"`
}

// daemonSnapshot captures the daemon device state under a single RLock so all
// fields are mutually consistent (no TOCTOU between connected vs. device counts).
// Shared by Status() (takes connected[0]) and writeDaemonStatus() (takes all).
type daemonSnapshot struct {
	connected []connectedSnapshot // active sessions, sorted by serial
	serials   []string            // all registered device serials (keys from devices map)
}

// snapshotDaemon returns a consistent snapshot of all device actors.
// The sort matters: handleConn's auto-detect picks conns[0] when no device is
// specified, so an unsorted map iteration would make "phonefast tap" with
// multiple devices non-deterministically target a random one.
func (d *Daemon) snapshotDaemon() daemonSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	snap := daemonSnapshot{
		connected: make([]connectedSnapshot, 0, len(d.devices)),
		serials:   make([]string, 0, len(d.devices)),
	}
	for _, a := range d.devices {
		snap.serials = append(snap.serials, a.serial)
		as, _ := a.status.Load().(*ActorStatus)
		if as == nil || !as.Connected {
			continue
		}
		snap.connected = append(snap.connected, connectedSnapshot{
			Connected:    as.Connected,
			Serial:       as.Serial,
			DeviceWidth:  as.DeviceWidth,
			DeviceHeight: as.DeviceHeight,
			ControlAvail: as.ControlAvail,
			UIAvail:      as.UIAvail,
		})
	}
	sort.Slice(snap.connected, func(i, j int) bool { return snap.connected[i].Serial < snap.connected[j].Serial })
	sort.Strings(snap.serials)
	return snap
}

// getOrCreateActor returns the DeviceActor for the given serial, creating one
// lazily if it doesn't exist. Thread-safe.
//
// Concurrency model:
//   - Different devices connect in parallel (no shared lock between them).
//   - The SAME device connects serially: a per-serial mutex guarantees only one
//     newDeviceActor runs for a given serial at a time. Without this, two
//     concurrent first-requests for one device would each call session.Connect,
//     and Connect kills the device's existing scrcpy server (pkill by serial,
//     not by scid) — so the loser would tear down the winner's server.
//
// The ~2.5s handshake runs OUTSIDE d.mu so a slow connect on one device never
// blocks another device's RLock fast path or the accept loop.
//
// Actors are stored under actor.serial (resolved by connectDevice), not the
// caller's serial param. Lookups are direct map lookups by that key.
func (d *Daemon) getOrCreateActor(serial string) (*DeviceActor, error) {
	// Fast path: actor already exists. Direct map lookup by caller serial —
	// map keys are always actor.serial (see comment at insertion below).
	d.mu.RLock()
	if actor, ok := d.devices[serial]; ok && actor != nil {
		d.mu.RUnlock()
		return actor, nil
	}
	d.mu.RUnlock()

	// Serialize same-device connects. Different serials get different mutexes
	// and proceed in parallel.
	serialMu := d.connectMutex(serial)
	serialMu.Lock()
	defer serialMu.Unlock()

	// Re-check under the per-serial lock: a prior holder of this same mutex may
	// have just finished creating the actor.
	d.mu.RLock()
	if actor, ok := d.devices[serial]; ok && actor != nil {
		d.mu.RUnlock()
		return actor, nil
	}
	d.mu.RUnlock()

	// Connect outside d.mu. newDeviceActor allocates a scid and does the
	// device handshake synchronously; on failure it has already released its
	// own scid (see actor.go), so nothing here needs cleanup on the error path.
	actor, err := newDeviceActor(serial, d.scidAlloc)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	// Defensive double-check: another goroutine may have created an actor for
	// the same device via a different caller-serial (e.g. "" vs "ABC123" both
	// resolving to "ABC123"). The per-serial mutex does NOT cover this — the
	// two callers hold DIFFERENT mutexes. Discard our duplicate: close its
	// session and release its scid before returning the winner.
	if existing, ok := d.devices[actor.serial]; ok && existing != nil {
		d.mu.Unlock()
		if actor.session != nil {
			actor.session.Close()
		}
		d.scidAlloc.Release(actor.scid)
		return existing, nil
	}
	// Store under actor.serial (resolved by connectDevice), not the caller's
	// param. In edge cases (e.g. empty-serial auto-detect) they may differ.
	d.devices[actor.serial] = actor
	d.wg.Add(1)
	d.mu.Unlock()

	go actor.run(d.ctx, &d.wg)
	phonelog.Default().Write("device actor created: %s (scid=%x)", actor.serial, actor.scid)
	return actor, nil
}

// connectMutex returns the per-serial mutex used to serialize same-device
// connects. The map of mutexes is itself guarded by connectMuMu.
func (d *Daemon) connectMutex(serial string) *sync.Mutex {
	d.connectMuMu.Lock()
	defer d.connectMuMu.Unlock()
	mu, ok := d.connectMu[serial]
	if !ok {
		mu = &sync.Mutex{}
		d.connectMu[serial] = mu
	}
	return mu
}

// isConnectionlessMethod reports whether an RPC method can be answered
// without binding a per-device session. status reports daemon-level info;
// list_devices is a pure ADB scan; connect/disconnect are daemon-control
// stubs (rejected in Dispatch); wait is a pure local sleep handled in
// handleConn (NOT dispatched to the actor — a daemon-side sleep on the
// actor's single-threaded loop would block every other request to that
// device). Binding a session for any of these would be a side effect for
// what should be a cheap or rejected call.
func isConnectionlessMethod(method string) bool {
	switch method {
	case "status", "list_devices", "connect", "disconnect", "wait":
		return true
	}
	return false
}

// writeDaemonStatus writes daemon-level status (no device context) to the
// connection. Used when the "status" method is called without a device serial.
//
// "connected" is true if at least one managed device actor is currently
// connected (mirrors the Status() semantics), so a status probe against a
// daemon that does have live devices no longer falsely reports connected=false.
//
// All fields come from a single daemonSnapshot so device_count, devices, and
// connected_devices are mutually consistent (no TOCTOU between reads).
func writeDaemonStatus(conn net.Conn, id int64, d *Daemon) {
	snap := d.snapshotDaemon()

	info := map[string]any{
		"connected":         len(snap.connected) > 0,
		"pid":               os.Getpid(),
		"socket_path":       d.socketPath,
		"started_at":        d.startedAt,
		"device_count":      len(snap.serials),
		"devices":           snap.serials,
		"connected_devices": snap.connected,
	}
	writeResponse(conn, newResultResponse(id, info))
}

// writeResponse marshals a JSON-RPC response, frames it with a newline, and
// writes it to the connection. Shared by writeError, writeDaemonStatus, and
// the inline response paths in handleConn.
func writeResponse(conn net.Conn, resp *Response) {
	if resp.streamPayload != nil {
		writeStreamedResponse(conn, resp)
		return
	}
	respBytes, _ := json.Marshal(resp)
	respBytes = append(respBytes, '\n')
	conn.Write(respBytes)
}

// writeStreamedResponse writes a response carrying a large base64 image
// field without materializing the base64 string or the marshaled envelope:
//
//	{"jsonrpc":"2.0","result":<prefix> <b64 chunks> <suffix>,"id":N}\n
//
// Only small framing allocations occur (bufio + base64's internal state).
// The emitted bytes are identical to the non-streaming path.
func writeStreamedResponse(conn net.Conn, resp *Response) {
	bw := bufio.NewWriterSize(conn, 32*1024)
	bw.WriteString(`{"jsonrpc":"2.0","result":`)
	bw.Write(resp.streamPrefix)
	enc := base64.NewEncoder(base64.StdEncoding, bw)
	enc.Write(resp.streamPayload)
	enc.Close()
	bw.Write(resp.streamSuffix)
	fmt.Fprintf(bw, `,"id":%d}`, resp.ID)
	bw.WriteByte('\n')
	bw.Flush()
}

// Start connects to the device, opens the Unix socket, and serves requests.
// Blocks until ctx is cancelled or a fatal error occurs.
func (d *Daemon) Start(ctx context.Context) error {
	// Set up signal handling
	ctx, d.cancel = context.WithCancel(ctx)
	d.ctx = ctx
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	// Wire daemon-level OCR service into RPC dispatch.
	SetOCRService(d.ocrService)

	// Optional pprof endpoint for memory/CPU profiling (debug builds).
	// Set PHONEFAST_PPROF=localhost:6060 to enable; unset = zero overhead.
	// Uses http.DefaultServeMux for automatic pprof handler registration.
	if addr := os.Getenv("PHONEFAST_PPROF"); addr != "" {
		d.pprofServer = &http.Server{Addr: addr}
		go func() {
			phonelog.Default().Write("pprof listening on %s", addr)
			if err := d.pprofServer.ListenAndServe(); err != http.ErrServerClosed {
				phonelog.Default().Write("pprof server stopped: %v", err)
			}
		}()
	}

	// OCR is lazy: the engine + PP-OCR models (~60-90MB) load on the first
	// OCR RPC, not at daemon startup. This keeps the daemon's baseline memory
	// low for the common case where OCR is never used (most CLI/MCP/agent
	// flows rely on screenshot + UI tree, not OCR). Set PHONEFAST_OCR_WARMUP=1
	// to eagerly load at startup instead (e.g. a long-lived server that is
	// known to use OCR and wants to avoid the ~3.7s first-call latency).
	if os.Getenv("PHONEFAST_OCR_WARMUP") == "1" {
		go func() {
			if err := d.ocrService.Warmup(); err != nil {
				phonelog.Default().Write("OCR warmup deferred: %v", err)
			} else {
				phonelog.Default().Write("OCR warmup complete")
			}
		}()
	}

	go func() {
		select {
		case sig := <-sigCh:
			phonelog.Default().Write("received %v, shutting down...", sig)
			d.cancel()
		case <-ctx.Done():
		}
	}()

	// Acquire exclusive daemon lock via PID file flock. This prevents a second
	// daemon instance (from ANY binary path) from starting while one is already
	// running. Without this, two daemons race on os.Remove+net.Listen and the
	// loser silently deletes the winner's socket file.
	if err := d.acquireLock(); err != nil {
		d.teardownActor()
		return err
	}

	// Remove stale socket file
	os.Remove(d.socketPath)

	// devices map is initialized in New(); actors are created lazily on first
	// request (see getOrCreateActor), not eagerly at startup.

	// Create Unix socket listener
	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		d.teardownActor()
		return fmt.Errorf("listen unix socket: %w", err)
	}
	d.listener = listener

	// Restrict socket permissions to current user
	os.Chmod(d.socketPath, 0600)

	// Write PID directly to the already-locked PID file (flock'd in acquireLock).
	// The PID file serves as both lock and PID record, so we can't use WritePID
	// (which creates a new file via temp+rename, breaking the flock).
	if err := d.writePIDLocked(); err != nil {
		listener.Close()
		d.listener = nil
		d.teardownActor()
		return fmt.Errorf("write pid file: %w", err)
	}

	d.startedAt = time.Now().Format(time.RFC3339)
	phonelog.Default().Write("daemon ready: socket=%s pid=%d", d.socketPath, os.Getpid())

	// Enter accept loop. The serve goroutine is tracked in d.wg so that
	// cleanup()'s Wait() cannot return while serve is still mid-Add for a
	// just-accepted connection (which would be a WaitGroup Add-after-Wait
	// race). serve's own Done fires only after its last per-conn Add.
	d.wg.Add(1)
	serveErr := make(chan error, 1)
	go func() {
		defer d.wg.Done()
		serveErr <- d.serve(d.ctx)
	}()

	// Wait for shutdown signal or serve error
	select {
	case <-ctx.Done():
		phonelog.Default().Write("shutting down...")
	case err := <-serveErr:
		if err != nil {
			phonelog.Default().Write("serve error: %v", err)
		}
		d.cancel()
	}

	return d.cleanup()
}

// serve accepts connections and handles them in goroutines.
func (d *Daemon) serve(ctx context.Context) error {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil // graceful shutdown
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}

		d.wg.Add(1)
		go d.handleConn(ctx, conn)
	}
}

// handleConn reads a single JSON-RPC request, dispatches it to the device
// actor via channel, waits for the response, and writes it back.
func (d *Daemon) handleConn(ctx context.Context, conn net.Conn) {
	defer d.wg.Done()
	defer conn.Close()

	conn.SetReadDeadline(deadline(ctx, 30))

	// When the daemon shuts down (ctx cancelled), release any blocked read
	// immediately rather than waiting up to 30 seconds for the deadline.
	readDone := make(chan struct{})
	defer close(readDone)
	go func() {
		select {
		case <-ctx.Done():
			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		case <-readDone:
		}
	}()

	reader := bufio.NewReader(conn)
	reqBytes, err := reader.ReadBytes('\n')
	if err != nil {
		writeError(conn, 0, ErrParse, fmt.Sprintf("read request: %v", err))
		return
	}

	var req Request
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		writeError(conn, 0, ErrParse, fmt.Sprintf("parse request: %v", err))
		return
	}

	// Extract target device serial from RPC params. If not set, auto-detect the
	// first connected device via ADB. Connectionless methods (status /
	// list_devices / connect / disconnect) skip device binding.
	//
	// We use adb.ListDevices()[0] rather than an already-connected actor for
	// determinism: ADB's device order is stable across calls, so the same
	// device-less command always targets the same device. Picking from
	// d.devices (a map) would be non-deterministic, and mixing the two sources
	// (ADB order vs sorted-actor order) could make the first call target device
	// X and subsequent calls device Y.
	connectionless := isConnectionlessMethod(req.Method)
	deviceSerial := parseStringParam(req.Params, "device")
	if deviceSerial == "" && !connectionless {
		if devs, err := adb.ListDevices(); err == nil && len(devs) > 0 {
			deviceSerial = devs[0].Serial
		}
	}
	if deviceSerial == "" && !connectionless {
		writeError(conn, req.ID, ErrNoDevice, "no device specified and none connected")
		return
	}

	// Lazily create or retrieve the actor for this device. Connectionless
	// methods (status / list_devices) skip actor creation entirely: a status
	// probe must not force a 2.5s connect, and list_devices is a pure ADB scan.
	var actor *DeviceActor
	if deviceSerial != "" && !connectionless {
		var err error
		actor, err = d.getOrCreateActor(deviceSerial)
		if err != nil {
			writeError(conn, req.ID, ErrNoDevice, fmt.Sprintf("connect device: %v", err))
			return
		}
	}

	// No actor means a connectionless method with no device param.
	if actor == nil {
		// "status" reports daemon-level info (pid, device_count,
		// connected_devices) — handled here, not via Dispatch, because that
		// info lives on *Daemon.
		if req.Method == "status" {
			writeDaemonStatus(conn, req.ID, d)
			return
		}
		// "wait" sleeps in this handleConn goroutine — NOT on the device
		// actor's single-threaded loop. It has no device-side effect, so
		// blocking here (one goroutine per connection) never stalls other
		// requests to the device, and concurrent connections proceed in
		// parallel. The duration is capped so a misbehaving caller can't pin
		// many goroutines for long periods; daemon shutdown (ctx.Done)
		// interrupts the sleep immediately.
		if req.Method == "wait" {
			const maxWaitMs = 60_000
			ms := parseIntParam(req.Params, "duration_ms")
			if ms <= 0 {
				ms = 1000
			}
			if ms > maxWaitMs {
				ms = maxWaitMs
			}
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-ctx.Done():
			}
			writeResponse(conn, newResultResponse(req.ID, map[string]any{
				"message": fmt.Sprintf("Waited %dms", ms),
			}))
			return
		}
		// Other connectionless methods (list_devices, connect/disconnect)
		// dispatch fine with a nil session.
		conn.SetWriteDeadline(deadline(ctx, 10))
		writeResponse(conn, Dispatch(nil, &req))
		return
	}

	// Send request to the actor goroutine with timeout.
	// If the actor is stuck (reqCh full), fail rather than hang forever.
	replyCh := make(chan *Response, 1)
	ar := actorRequest{req: &req, replyCh: replyCh}

	sendTimer := time.NewTimer(35 * time.Second)
	defer sendTimer.Stop()

	select {
	case actor.reqCh <- ar:
		// Sent successfully — wait for reply below
	case <-ctx.Done():
		writeError(conn, req.ID, ErrInternal, "daemon shutting down")
		return
	case <-sendTimer.C:
		writeError(conn, req.ID, ErrTimeout, "device actor busy")
		return
	}

	// Wait for reply with timeout
	replyTimer := time.NewTimer(60 * time.Second)
	defer replyTimer.Stop()

	var resp *Response
	select {
	case resp = <-replyCh:
		// Got reply
	case <-ctx.Done():
		writeError(conn, req.ID, ErrInternal, "daemon shutting down")
		return
	case <-replyTimer.C:
		writeError(conn, req.ID, ErrTimeout, "request timed out")
		return
	}

	conn.SetWriteDeadline(deadline(ctx, 10))
	writeResponse(conn, resp)
}

// Stop gracefully shuts down the daemon: stops accepting, waits for
// in-flight requests, closes the session, and removes socket/PID files.
func (d *Daemon) Stop() error {
	if d.cancel != nil {
		d.cancel()
	}
	return d.cleanup()
}

// writePIDLocked writes the current PID into the already-locked PID file.
// The PID file was opened and flock'd in acquireLock; we write directly to it
// (no temp+rename, which would break the lock).
func (d *Daemon) writePIDLocked() error {
	if d.lockFile == nil {
		return fmt.Errorf("pid file not locked")
	}
	if err := d.lockFile.Truncate(0); err != nil {
		return fmt.Errorf("truncate pid file: %w", err)
	}
	if _, err := d.lockFile.Seek(0, 0); err != nil {
		return fmt.Errorf("seek pid file: %w", err)
	}
	content := fmt.Sprintf("%d\n", os.Getpid())
	if _, err := d.lockFile.WriteString(content); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	return nil
}

// teardownActor cancels the context, waits for the actor goroutine to exit
// (closing its session), releases the actor's scid back to the allocator, and
// clears the devices map. Used on Start() failure paths so a half-initialized
// daemon leaves no stale state.
func (d *Daemon) teardownActor() {
	if d.cancel != nil {
		d.cancel()
	}
	d.wg.Wait()
	d.releaseLock()
	d.mu.Lock()
	for _, a := range d.devices {
		d.scidAlloc.Release(a.scid)
	}
	// Clear the map (don't set to nil — New() guarantees non-nil).
	for k := range d.devices {
		delete(d.devices, k)
	}
	d.mu.Unlock()
}

// cleanup performs orderly shutdown.
func (d *Daemon) cleanup() error {
	// Stop accepting new connections
	if d.listener != nil {
		d.listener.Close()
		d.listener = nil
	}

	// Wait for in-flight requests AND actor goroutines to complete.
	// Actor goroutines see ctx.Done() and exit, closing their sessions
	// in their deferred cleanup.
	d.wg.Wait()

	// Close daemon-level OCR service (releases engine models). ocrService is
	// always set by New(), so no nil guard needed.
	d.ocrService.Close()

	// Release daemon lock (flock is released when the file is closed).
	d.releaseLock()

	// Shut down pprof server if enabled.
	if d.pprofServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		d.pprofServer.Shutdown(ctx)
		cancel()
		d.pprofServer = nil
	}

	// Remove socket and PID files (both current serial-specific and legacy UID-only)
	os.Remove(d.socketPath)
	RemovePID(d.pidFile)
	os.Remove(SocketName())
	RemovePID(PidFileName())

	phonelog.Default().Write("daemon stopped")
	return nil
}

// ── Helpers ──

func writeError(conn net.Conn, id int64, code int, msg string) {
	writeResponse(conn, newErrorResponse(id, code, msg))
}

func deadline(ctx context.Context, seconds int) time.Time {
	select {
	case <-ctx.Done():
		return time.Now().Add(1 * time.Second)
	default:
		return time.Now().Add(time.Duration(seconds) * time.Second)
	}
}
