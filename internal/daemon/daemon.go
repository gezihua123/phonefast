package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

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
	serial    string                  // default device serial
	scidAlloc *ScidAllocator          // assigns collision-free scids to actors

	listener   net.Listener
	pidFile    string
	socketPath string
	startedAt  string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Config holds daemon startup settings.
type Config struct {
	Serial     string // device serial; empty = first available
	Foreground bool   // stay in foreground (don't daemonize)
	SocketPath string // override default socket path
	PidFile    string // override default pid file path
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
	socketPath := cfg.SocketPath
	if socketPath == "" {
		socketPath = SocketName(cfg.Serial)
	}
	pidFile := cfg.PidFile
	if pidFile == "" {
		pidFile = PidFileName(cfg.Serial)
	}

	return &Daemon{
		serial:     cfg.Serial,
		scidAlloc:  NewScidAllocator(),
		socketPath: socketPath,
		pidFile:    pidFile,
	}
}

// Status returns current daemon runtime info.
func (d *Daemon) Status() StatusInfo {
	d.mu.RLock()
	actor, ok := d.devices[d.serial]
	d.mu.RUnlock()

	s := StatusInfo{
		SocketPath: d.socketPath,
		Pid:        os.Getpid(),
		StartedAt:  d.startedAt,
	}

	if ok {
		// Two-value assertion: never panics even if status was never Stored.
		if as, _ := actor.status.Load().(*ActorStatus); as != nil {
			s.Connected = as.Connected
			s.Serial = as.Serial
			s.DeviceWidth = as.DeviceWidth
			s.DeviceHeight = as.DeviceHeight
			s.ControlAvail = as.ControlAvail
			s.UIAvail = as.UIAvail
		}
	}

	return s
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

	go func() {
		select {
		case sig := <-sigCh:
			phonelog.Default().Write("received %v, shutting down...", sig)
			d.cancel()
		case <-ctx.Done():
		}
	}()

	// Remove stale socket file
	os.Remove(d.socketPath)

	// Create device actor (connects to device synchronously).
	// connectDevice() inside newDeviceActor handles serial auto-detection
	// when d.serial is empty. The scid is allocated internally so its
	// forwarded port won't collide with any other actor in this daemon.
	actor, err := newDeviceActor(d.serial, d.scidAlloc)
	if err != nil {
		return fmt.Errorf("connect device: %w", err)
	}

	// Update the resolved serial (needed when d.serial was empty)
	if d.serial == "" {
		d.serial = actor.serial
	}

	// Register the actor
	d.mu.Lock()
	if d.devices == nil {
		d.devices = make(map[string]*DeviceActor)
	}
	d.devices[d.serial] = actor
	d.mu.Unlock()

	// Start the actor's event loop (replaces healthLoop)
	d.wg.Add(1)
	go actor.run(d.ctx, &d.wg)

	// Create Unix socket listener
	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		d.teardownActor()
		return fmt.Errorf("listen unix socket: %w", err)
	}
	d.listener = listener

	// Restrict socket permissions to current user
	os.Chmod(d.socketPath, 0600)

	// Write PID file
	if err := WritePID(d.pidFile); err != nil {
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

	// Look up the device actor
	d.mu.RLock()
	actor, ok := d.devices[d.serial]
	d.mu.RUnlock()

	if !ok {
		writeError(conn, req.ID, ErrNoDevice, "no device actor registered")
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
	respBytes, _ := json.Marshal(resp)
	respBytes = append(respBytes, '\n')
	conn.Write(respBytes)
}

// Stop gracefully shuts down the daemon: stops accepting, waits for
// in-flight requests, closes the session, and removes socket/PID files.
func (d *Daemon) Stop() error {
	if d.cancel != nil {
		d.cancel()
	}
	return d.cleanup()
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
	d.mu.Lock()
	for _, a := range d.devices {
		d.scidAlloc.Release(a.scid)
	}
	d.devices = nil
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

	// Remove socket and PID files (both current serial-specific and legacy UID-only)
	os.Remove(d.socketPath)
	RemovePID(d.pidFile)
	os.Remove(DefaultSocketName())
	RemovePID(DefaultPidFileName())

	phonelog.Default().Write("daemon stopped")
	return nil
}

// ── Helpers ──

func writeError(conn net.Conn, id int64, code int, msg string) {
	resp := newErrorResponse(id, code, msg)
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	conn.Write(data)
}

func deadline(ctx context.Context, seconds int) time.Time {
	select {
	case <-ctx.Done():
		return time.Now().Add(1 * time.Second)
	default:
		return time.Now().Add(time.Duration(seconds) * time.Second)
	}
}
