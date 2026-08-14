package daemon

import (
	"context"
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

	phonelog "github.com/gezihua123/phonefast/internal/log"
	"github.com/gezihua123/phonefast/ocr"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// Daemon is a long-running process that holds device sessions and serves
// JSON-RPC requests over a Unix domain socket.
//
// Each device is managed by a DeviceActor goroutine that exclusively owns
// its session — no mutex is needed for session access. Communication between
// the accept loop and device actors goes through channels.
type Daemon struct {
	devices   map[string]*DeviceActor           // serial → device actor
	mu        sync.RWMutex                      // protects map access only
	scidAlloc *ScidAllocator                    // assigns collision-free scids to actors
	connectFn func(string, int) (Device, error) // device connect impl (injected; tests substitute)

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
	dispatcher *Dispatcher  // RPC dispatch (holds the OCR service reference)

	listener   net.Listener
	pidFile    string
	socketPath string
	startedAt  string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	lockFile    *os.File     // PID file handle held for the process lifetime (flock)
	pprofServer *http.Server // non-nil when pprof is enabled (PHONEFAST_PPROF)
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
	ocrService := ocr.NewService(ocr.Config{
		Engine:    os.Getenv("PHONEFAST_OCR_ENGINE"), // "onnx" (default) | "ncnn"
		UseVision: os.Getenv("PHONEFAST_OCR_VISION") != "false",
	})
	return &Daemon{
		devices:    make(map[string]*DeviceActor),
		scidAlloc:  NewScidAllocator(),
		connectFn:  connectDevice,
		connectMu:  make(map[string]*sync.Mutex),
		ocrService: ocrService,
		dispatcher: NewDispatcher(ocrService),
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
	actor, err := newDeviceActor(serial, d.scidAlloc, d.dispatcher, d.connectFn)
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
		if actor.device != nil {
			actor.device.Close()
		}
		d.scidAlloc.Release(actor.scid)
		return existing, nil
	}
	// Store under actor.serial (resolved by connectDevice), not the caller's
	// param. In edge cases (e.g. empty-serial auto-detect) they may differ.
	d.devices[actor.serial] = actor
	d.wg.Add(1)

	// Give the actor its own context derived from the daemon context so it can
	// be stopped independently (disconnect one device) while daemon cancel still
	// cascades to all actors on full shutdown.
	actor.ctx, actor.cancel = context.WithCancel(d.ctx)
	d.mu.Unlock()

	go actor.run(actor.ctx, &d.wg)
	phonelog.Default().Write("device actor created: %s (scid=%x)", actor.serial, actor.scid)
	return actor, nil
}

// removeDevice stops a single device actor, closes its session, releases its
// scid, and removes it from the daemon's device map. It is safe to call on a
// serial that is not currently managed (returns an error).
func (d *Daemon) removeDevice(serial string) error {
	d.mu.Lock()
	actor, ok := d.devices[serial]
	if !ok || actor == nil {
		d.mu.Unlock()
		return fmt.Errorf("device not managed: %s", serial)
	}
	delete(d.devices, serial)
	delete(d.connectMu, serial)
	d.mu.Unlock()

	actor.stop()
	d.scidAlloc.Release(actor.scid)
	phonelog.Default().Write("device actor removed: %s (scid=%x)", serial, actor.scid)
	return nil
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
// list_devices is a pure ADB scan; connect/disconnect manage devices at
// the daemon level (create/remove actors in d.devices — not per-session
// operations); wait is a pure local sleep handled in handleConn (NOT
// dispatched to the actor — a daemon-side sleep on the actor's
// single-threaded loop would block every other request to that device).
// Binding a session for any of these would be a side effect for what
// should be a cheap or daemon-level call.
func isConnectionlessMethod(method string) bool {
	switch method {
	case protocol.MethodStatus, protocol.MethodListDevices, protocol.MethodConnect, protocol.MethodDisconnect, protocol.MethodWait:
		return true
	}
	return false
}

// Start connects to the device, opens the Unix socket, and serves requests.
// Blocks until ctx is cancelled or a fatal error occurs.
func (d *Daemon) Start(ctx context.Context) error {
	// Signal handling
	ctx, d.cancel = context.WithCancel(ctx)
	d.ctx = ctx
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

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
