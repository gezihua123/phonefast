package daemon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	phonelog "github.com/gezihua123/phonefast/internal/log"
)

// Supervisor owns daemon lifecycle: background spawn, socket wait, restart
// dedup, and graceful stop. One instance per process; the CLI passes its
// EnsureRunning to the RPC Client as the mid-RPC-crash ensurer.
//
// This moved OUT of cmd/phonefast so the daemon package can start, stop, and
// recover itself — previously the spawn/wait/kill choreography lived in the
// CLI layer while the daemon package owned the socket/pidfile primitives,
// inverting the dependency direction.
type Supervisor struct {
	cfg SupervisorConfig

	mu       sync.Mutex
	inFlight bool          // a restart is running (winner)
	done     chan struct{} // closed when the in-flight restart finishes
	lastErr  error         // result of the most recent restart attempt
}

// SupervisorConfig configures a Supervisor. Zero-valued durations fall back
// to the production defaults (Poll 200ms, MaxWait 8s, DeathGrace 2s).
type SupervisorConfig struct {
	// ChildArgs are the executable args for the daemon child process,
	// e.g. ["daemon_worker"]. Defaults to ["daemon_worker"].
	ChildArgs []string

	// ExtraEnv is appended to the inherited environment for spawned children
	// (nil = inherit unchanged). The CLI passes PHONEFAST_OCR_* here.
	ExtraEnv []string

	// Poll is the socket-wait poll interval (0 → 200ms).
	Poll time.Duration
	// MaxWait is the socket-wait budget (0 → 8s).
	MaxWait time.Duration
	// DeathGrace is the grace period after the spawned child dies before
	// giving up (covers a concurrently-launched daemon winning the flock
	// race) (0 → 2s).
	DeathGrace time.Duration

	// SocketPath/PIDFile override the default paths (test hooks).
	SocketPath string
	PIDFile    string

	// EnvFile overrides the persisted-env file path (test hook). Empty uses
	// the default per-uid /tmp path.
	EnvFile string

	// Stderr receives progress messages ("Starting daemon..."); nil → os.Stderr.
	Stderr io.Writer

	// Spawn overrides the child-spawn implementation (test hook).
	Spawn func(extraEnv []string) (*exec.Cmd, error)
}

// NewSupervisor creates a supervisor for the given configuration.
func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	return &Supervisor{cfg: cfg}
}

func (s *Supervisor) socketPath() string {
	if s.cfg.SocketPath != "" {
		return s.cfg.SocketPath
	}
	return SocketName()
}

func (s *Supervisor) pidFile() string {
	if s.cfg.PIDFile != "" {
		return s.cfg.PIDFile
	}
	return PidFileName()
}

// envFile persists the extra env passed to Spawn so a later auto-restart
// (from a DIFFERENT CLI invocation that never saw the flags) can pass the
// same env to the replacement child. Without it, `phonefast daemon
// --ocr-engine apple` followed by a crash would silently restart with the
// default engine.
func (s *Supervisor) envFile() string {
	if s.cfg.EnvFile != "" {
		return s.cfg.EnvFile
	}
	return fmt.Sprintf("/tmp/phonefast-%d.env", os.Getuid())
}

func (s *Supervisor) poll() time.Duration {
	if s.cfg.Poll > 0 {
		return s.cfg.Poll
	}
	return 200 * time.Millisecond
}

func (s *Supervisor) maxWait() time.Duration {
	if s.cfg.MaxWait > 0 {
		return s.cfg.MaxWait
	}
	return 8 * time.Second
}

func (s *Supervisor) deathGrace() time.Duration {
	if s.cfg.DeathGrace > 0 {
		return s.cfg.DeathGrace
	}
	return 2 * time.Second
}

func (s *Supervisor) childArgs() []string {
	if len(s.cfg.ChildArgs) > 0 {
		return s.cfg.ChildArgs
	}
	return []string{"daemon_worker"}
}

func (s *Supervisor) print(format string, args ...any) {
	w := s.cfg.Stderr
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format, args...)
}

// EnsureRunning auto-starts the daemon if it isn't running and healthy, and
// waits for its socket to appear. Concurrent calls collapse into ONE restart
// attempt: the winner runs the restart, waiters block on a channel (no 50ms
// busy-poll) and re-check health when it finishes; a waiter whose wait
// resolved into a failed restart may attempt once as the new winner
// (preserving the chained-restart semantics). Each caller attempts at most
// one restart of its own. Returns nil once the daemon is up.
func (s *Supervisor) EnsureRunning() error {
	attempted := false
	for {
		if s.healthy() {
			return nil
		}
		s.mu.Lock()
		if s.inFlight {
			done := s.done
			s.mu.Unlock()
			<-done
			continue
		}
		if attempted {
			s.mu.Unlock()
			return s.lastErr
		}
		attempted = true
		s.inFlight = true
		s.done = make(chan struct{})
		s.mu.Unlock()

		err := s.restartOnce()

		s.mu.Lock()
		s.inFlight = false
		s.lastErr = err
		close(s.done)
		s.mu.Unlock()

		if err != nil {
			return err
		}
	}
}

// Spawn starts a detached daemon child and returns its pid. Used by
// `phonefast daemon` (background start). The caller is responsible for the
// "already running" pre-check.
func (s *Supervisor) Spawn(extraEnv []string) (int, error) {
	child, err := s.spawnDaemonWorker(extraEnv)
	if err != nil {
		return 0, err
	}
	// Persist the env so a later auto-restart passes the same configuration.
	if len(extraEnv) > 0 {
		if werr := os.WriteFile(s.envFile(), []byte(strings.Join(extraEnv, "\n")), 0644); werr != nil {
			s.print("Warning: cannot persist daemon env: %v\n", werr)
		}
	}
	return child.Process.Pid, nil
}

// Stop gracefully stops the daemon (SIGTERM, ~5s wait, force-kill fallback)
// and returns the user-facing status message ("daemon stopped" /
// "daemon killed"). A missing PID file is an error; a stale PID file is
// cleaned up silently (message printed to Stderr) with no error.
func (s *Supervisor) Stop() (string, error) {
	pidFile := s.pidFile()
	pid, err := ReadPID(pidFile)
	if err != nil || pid == 0 {
		return "", fmt.Errorf("daemon not running (no PID file)")
	}

	if !IsProcessAlive(pid) {
		s.print("daemon not running (stale PID file)\n")
		RemovePID(pidFile)
		os.Remove(s.socketPath())
		return "", nil
	}

	killDaemon(pid)

	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if !IsProcessAlive(pid) {
			os.Remove(s.envFile())
			return "daemon stopped", nil
		}
	}

	s.print("daemon not responding, force killing...\n")
	killDaemon(pid)
	time.Sleep(500 * time.Millisecond)
	RemovePID(pidFile)
	os.Remove(s.socketPath())
	os.Remove(s.envFile())
	return "daemon killed", nil
}

// healthy reports whether the daemon is running and responding on its socket.
func (s *Supervisor) healthy() bool {
	pidFile := s.pidFile()
	pid, err := ReadPID(pidFile)
	if err != nil || pid <= 0 || !IsProcessAlive(pid) {
		return false
	}
	client := s.probeClient()
	_, err = client.Ping()
	return err == nil
}

// probeClient builds a Client bound to the supervised socket for health
// probes. The 30s timeout mirrors NewClient — a zero-value Client would set
// an already-expired read deadline and fail every ping.
func (s *Supervisor) probeClient() *Client {
	return &Client{socketPath: s.socketPath(), timeout: 30 * time.Second}
}

// restartOnce performs one full restart attempt: health-check the existing
// daemon (force-stopping an unresponsive one), clean stale files, spawn the
// child, and wait for its socket.
func (s *Supervisor) restartOnce() error {
	pidFile := s.pidFile()
	socketPath := s.socketPath()

	// Check if the daemon is already running and healthy.
	if pid, _ := ReadPID(pidFile); pid > 0 && IsProcessAlive(pid) {
		client := s.probeClient()
		if _, err := client.Ping(); err == nil {
			return nil // daemon is running and responding
		}
		s.print("Daemon unresponsive, restarting...\n")
		s.forceStop(pidFile, pid)
	}

	// Clean up stale files.
	RemovePID(pidFile)
	os.Remove(socketPath)

	s.print("Starting daemon...\n")

	// Daemon starts empty — no device connection needed at startup.
	// Device actors are created lazily on first request.
	extraEnv := s.cfg.ExtraEnv
	if len(extraEnv) == 0 {
		// A prior explicit Spawn may have persisted its env (e.g.
		// `phonefast daemon --ocr-engine apple`) — reuse it so a restart
		// doesn't silently revert to default configuration.
		if data, rerr := os.ReadFile(s.envFile()); rerr == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) > 0 && lines[0] != "" {
				extraEnv = lines
			}
		}
	}
	child, err := s.spawnDaemonWorker(extraEnv)
	if err != nil {
		return err
	}

	childPid := child.Process.Pid
	return waitForSocket(socketPath,
		func() bool { return IsProcessAlive(childPid) },
		s.poll(), s.maxWait(), s.deathGrace())
}

// forceStop forcefully kills a daemon process and cleans up.
func (s *Supervisor) forceStop(pidFile string, pid int) {
	killDaemon(pid)
	time.Sleep(500 * time.Millisecond)
	if !IsProcessAlive(pid) {
		RemovePID(pidFile)
		os.Remove(s.socketPath())
		return
	}
	RemovePID(pidFile)
	os.Remove(s.socketPath())
}

// spawnDaemonWorker starts the daemon child process detached (own session via
// sysProcAttr, stdin/stdout to /dev/null). stderr is captured to the daemon
// stderr log (phonelog.DaemonStderrPath) instead of /dev/null: the shared
// phonelog only sees errors after the child's Go main initializes its logger,
// so a crash before that (CGO loader failure, runtime panic) would otherwise
// leave no trace anywhere. extraEnv is appended to the inherited environment
// (nil = inherit unchanged).
func (s *Supervisor) spawnDaemonWorker(extraEnv []string) (*exec.Cmd, error) {
	if s.cfg.Spawn != nil {
		return s.cfg.Spawn(extraEnv)
	}

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot find executable: %w", err)
	}
	devNull, err := devNullFile()
	if err != nil {
		return nil, fmt.Errorf("cannot open null device: %w", err)
	}
	defer devNull.Close() // child holds its own dup'd fd after Start

	// Diagnosability is best-effort: if the stderr log can't be opened, fall
	// back to /dev/null rather than failing the spawn.
	stderrFile, err := os.OpenFile(phonelog.DaemonStderrPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		stderrFile = devNull
	} else {
		defer stderrFile.Close()
	}

	child := exec.Command(exe, s.childArgs()...)
	child.Dir = filepath.Dir(exe)
	child.SysProcAttr = sysProcAttr()
	child.Stdin = devNull
	child.Stdout = devNull
	child.Stderr = stderrFile
	if len(extraEnv) > 0 {
		child.Env = append(os.Environ(), extraEnv...)
	}

	if err := child.Start(); err != nil {
		return nil, fmt.Errorf("failed to start daemon: %w", err)
	}
	return child, nil
}

// waitForSocket polls for the daemon socket until maxWait. childAlive reports
// whether the daemon child we spawned is still running. When our child dies,
// the socket can still appear: a concurrently-launched CLI's daemon may have
// won the flock and be mid-start (our child lost the race and exited). So on
// child death the deadline shrinks to deathGrace later instead of failing
// immediately — avoiding a needless direct-mode fallback while a healthy
// daemon is about to listen — while a genuinely failed daemon still reports
// within deathGrace rather than the full maxWait.
func waitForSocket(socketPath string, childAlive func() bool, poll, maxWait, deathGrace time.Duration) error {
	deadline := time.Now().Add(maxWait)
	var diedAt time.Time
	for time.Now().Before(deadline) {
		time.Sleep(poll)
		if _, err := os.Stat(socketPath); err == nil {
			return nil
		}
		if diedAt.IsZero() && !childAlive() {
			diedAt = time.Now()
			if g := diedAt.Add(deathGrace); g.Before(deadline) {
				deadline = g
			}
		}
	}
	return fmt.Errorf("daemon failed to start (see %s and %s for the startup error)",
		phonelog.DefaultPath(), phonelog.DaemonStderrPath())
}
