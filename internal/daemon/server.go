package daemon

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/gezihua123/phonefast/internal/adb"
	"github.com/gezihua123/phonefast/pkg/protocol"
)

// handleConn timeouts as package-level vars so tests can shrink them (they
// are deliberately long in production: a slow connect + screenshot pipeline
// must not be cut off, and the 60s reply window covers PNG→JPEG conversion
// on large frames).
var (
	actorSendTimeout  = 35 * time.Second
	actorReplyTimeout = 60 * time.Second
	maxWaitMs         = protocol.MaxWaitMs
)

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
func writeStreamedResponse(w io.Writer, resp *Response) {
	bw := bufio.NewWriterSize(w, 32*1024)
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
	// Accept both "serial" and "device" as the device selector.
	// The Go Client always sends "device", but raw RPC callers may send
	// "serial" — both are valid unique device identifiers.
	deviceSerial := parseStringParam(req.Params, "device")
	if deviceSerial == "" {
		deviceSerial = parseStringParam(req.Params, "serial")
	}
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
		if req.Method == protocol.MethodStatus {
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
		if req.Method == protocol.MethodWait {
			// Normalization (default + cap) is shared with the local CLI/MCP
			// waits via protocol.NormalizeWaitMs; only the sleep differs — the
			// daemon's is ctx-interruptible, so it can't use protocol.SleepWait.
			ms := protocol.NormalizeWaitMs(parseIntParam(req.Params, "duration_ms"), maxWaitMs)
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-ctx.Done():
			}
			writeResponse(conn, newResultResponse(req.ID, map[string]any{
				"message": protocol.FormatWaitResult(ms),
			}))
			return
		}
		// "connect" creates a DeviceActor for the specified device (lazy —
		// getOrCreateActor does the scrcpy handshake). If no device param is
		// given, it auto-detects the first ADB device.
		if req.Method == protocol.MethodConnect {
			if deviceSerial == "" {
				writeError(conn, req.ID, ErrNoDevice, "no device specified and none connected")
				return
			}
			actor, err := d.getOrCreateActor(deviceSerial)
			if err != nil {
				writeError(conn, req.ID, ErrNoDevice, fmt.Sprintf("connect device: %v", err))
				return
			}
			writeResponse(conn, newResultResponse(req.ID, map[string]any{
				"message": fmt.Sprintf("connected to %s", actor.serial),
				"serial":  actor.serial,
			}))
			return
		}
		// "disconnect" stops a single device actor and removes it from the
		// daemon's device map — shuts down only that device, not the daemon.
		if req.Method == protocol.MethodDisconnect {
			if deviceSerial == "" {
				writeError(conn, req.ID, ErrNoDevice, "no device specified")
				return
			}
			if err := d.removeDevice(deviceSerial); err != nil {
				writeError(conn, req.ID, ErrNoDevice, err.Error())
				return
			}
			writeResponse(conn, newResultResponse(req.ID, map[string]any{
				"message": fmt.Sprintf("disconnected %s", deviceSerial),
				"serial":  deviceSerial,
			}))
			return
		}
		// Other connectionless methods (list_devices) dispatch fine with a
		// nil session.
		conn.SetWriteDeadline(deadline(ctx, 10))
		writeResponse(conn, d.dispatcher.Dispatch(nil, &req))
		return
	}

	// Send request to the actor goroutine with timeout.
	// If the actor is stuck (reqCh full), fail rather than hang forever.
	replyCh := make(chan *Response, 1)
	ar := actorRequest{req: &req, replyCh: replyCh}

	sendTimer := time.NewTimer(actorSendTimeout)
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
	replyTimer := time.NewTimer(actorReplyTimeout)
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
