package daemon

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// shortSocketDir creates a short-path temp dir for unix sockets — macOS caps
// unix socket paths at ~104 bytes and t.TempDir() exceeds it.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	tdir, err := os.MkdirTemp("/tmp", "pf-sup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tdir) })
	return tdir
}

// startFakeDaemonAt creates a responding fake daemon socket at `socket` and a
// pidfile containing our own (alive) pid — enough for Supervisor.healthy() to
// report true. The responder replies to any request with a valid status
// result. Returns an error (never calls t.Fatal) so it is safe to call from
// spawn hooks running in non-test goroutines.
func startFakeDaemonAt(t *testing.T, socket, pidFile string) error {
	l, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listen fake daemon: %w", err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				c.Read(buf) // one request line
				c.Write([]byte(`{"jsonrpc":"2.0","result":{"connected":false},"id":1}` + "\n"))
			}(conn)
		}
	}()
	if err := WritePID(pidFile); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	return nil
}

// TestWaitForSocket covers the concurrent-auto-start semantics: after our
// spawned child dies (flock-race loser), the socket may still appear from a
// competing daemon — so we wait out the grace period rather than fail fast,
// but a genuinely failed daemon reports after grace, not the full maxWait.
func TestWaitForSocket(t *testing.T) {
	sock := filepath.Join(shortSocketDir(t), "test.sock")
	dead := func() bool { return false }
	alive := func() bool { return true }

	t.Run("socket already present", func(t *testing.T) {
		os.WriteFile(sock, []byte{}, 0600)
		defer os.Remove(sock)
		if err := waitForSocket(sock, dead, time.Millisecond, 50*time.Millisecond, 10*time.Millisecond); err != nil {
			t.Errorf("want nil, got %v", err)
		}
	})

	t.Run("dead child + no socket fails after grace, not maxWait", func(t *testing.T) {
		start := time.Now()
		err := waitForSocket(sock, dead, 2*time.Millisecond, 500*time.Millisecond, 30*time.Millisecond)
		if err == nil {
			t.Fatal("want error, got nil")
		}
		if el := time.Since(start); el < 30*time.Millisecond || el > 300*time.Millisecond {
			t.Errorf("elapsed %v outside [grace=30ms, maxWait=500ms)", el)
		}
	})

	t.Run("live child + no socket waits full maxWait", func(t *testing.T) {
		start := time.Now()
		err := waitForSocket(sock, alive, 2*time.Millisecond, 120*time.Millisecond, 20*time.Millisecond)
		if err == nil {
			t.Fatal("want error, got nil")
		}
		if el := time.Since(start); el < 120*time.Millisecond {
			t.Errorf("gave up after %v, before maxWait=120ms", el)
		}
	})

	t.Run("socket appearing during grace succeeds (flock-race winner)", func(t *testing.T) {
		go func() {
			time.Sleep(40 * time.Millisecond)
			os.WriteFile(sock, []byte{}, 0600)
		}()
		defer os.Remove(sock)
		// Our child died immediately, grace=200ms; competing daemon's socket
		// appears at ~40ms — must succeed, not degrade to direct mode.
		if err := waitForSocket(sock, dead, 2*time.Millisecond, 500*time.Millisecond, 200*time.Millisecond); err != nil {
			t.Errorf("want nil (socket from competing daemon), got %v", err)
		}
	})
}

// TestEnsureRunningHealthyShortCircuit verifies a healthy daemon never
// triggers a spawn.
func TestEnsureRunningHealthyShortCircuit(t *testing.T) {
	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "daemon.sock")
	pidFile := filepath.Join(dir, "daemon.pid")
	if err := startFakeDaemonAt(t, socket, pidFile); err != nil {
		t.Fatal(err)
	}

	spawnCalls := 0
	s := NewSupervisor(SupervisorConfig{
		SocketPath: socket,
		PIDFile:    pidFile,
		Spawn: func(extraEnv []string) (*exec.Cmd, error) {
			spawnCalls++
			return nil, fmt.Errorf("should not spawn")
		},
	})
	if err := s.EnsureRunning(); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if spawnCalls != 0 {
		t.Fatalf("Spawn called %d times, want 0 (healthy short-circuit)", spawnCalls)
	}
}

// TestEnsureRunningDedup verifies N concurrent EnsureRunning calls collapse
// into exactly ONE restart attempt (mutex + done-channel, no busy-poll).
func TestEnsureRunningDedup(t *testing.T) {
	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "daemon.sock")
	pidFile := filepath.Join(dir, "daemon.pid")

	var spawnCalls int32
	s := NewSupervisor(SupervisorConfig{
		SocketPath: socket,
		PIDFile:    pidFile,
		Spawn: func(extraEnv []string) (*exec.Cmd, error) {
			atomic.AddInt32(&spawnCalls, 1)
			if err := startFakeDaemonAt(t, socket, pidFile); err != nil {
				return nil, err
			}
			return &exec.Cmd{Process: &os.Process{Pid: 1}}, nil
		},
	})

	const N = 8
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = s.EnsureRunning()
		}(i)
	}
	wg.Wait()

	if n := atomic.LoadInt32(&spawnCalls); n != 1 {
		t.Fatalf("spawn called %d times, want exactly 1", n)
	}
	for i, e := range errs {
		if e != nil {
			t.Fatalf("caller %d got error: %v", i, e)
		}
	}
}

// TestEnsureRunningChainAfterFailedRestart verifies the chained-restart
// semantics: a caller whose first restart failed returns the error, and a
// subsequent call starts a fresh restart attempt.
func TestEnsureRunningChainAfterFailedRestart(t *testing.T) {
	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "daemon.sock")
	pidFile := filepath.Join(dir, "daemon.pid")

	spawnCalls := 0
	s := NewSupervisor(SupervisorConfig{
		SocketPath: socket,
		PIDFile:    pidFile,
		Spawn: func(extraEnv []string) (*exec.Cmd, error) {
			spawnCalls++
			if spawnCalls == 1 {
				return nil, fmt.Errorf("spawn failed")
			}
			if err := startFakeDaemonAt(t, socket, pidFile); err != nil {
				return nil, err
			}
			return &exec.Cmd{Process: &os.Process{Pid: 1}}, nil
		},
	})

	if err := s.EnsureRunning(); err == nil {
		t.Fatal("want error on first EnsureRunning (spawn failed)")
	}
	if err := s.EnsureRunning(); err != nil {
		t.Fatalf("second EnsureRunning: %v", err)
	}
	if spawnCalls != 2 {
		t.Fatalf("spawn calls = %d, want 2", spawnCalls)
	}
}

// TestEnsureRunningWaiterBecomesWinnerAfterFailedRestart covers the
// concurrent chained-restart path: the first winner's restart FAILS, and a
// waiter blocked on the done channel (the `<-done; continue` loop) must
// become the new winner and run its own restartOnce — which succeeds.
// Unlike TestEnsureRunningChainAfterFailedRestart (sequential fresh calls),
// this exercises the waiter→winner transition under concurrency: all N
// callers are in-flight together. The failed first winner returns the spawn
// error; every other caller must return nil, and spawn must run exactly
// twice (no third attempt once the daemon is healthy).
func TestEnsureRunningWaiterBecomesWinnerAfterFailedRestart(t *testing.T) {
	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "daemon.sock")
	pidFile := filepath.Join(dir, "daemon.pid")

	var spawnCalls int32
	s := NewSupervisor(SupervisorConfig{
		SocketPath: socket,
		PIDFile:    pidFile,
		Spawn: func(extraEnv []string) (*exec.Cmd, error) {
			n := atomic.AddInt32(&spawnCalls, 1)
			if n == 1 {
				// Slow failure so the other callers pile up on the done
				// channel (they must be WAITERS, not fresh callers).
				time.Sleep(20 * time.Millisecond)
				return nil, fmt.Errorf("spawn failed")
			}
			if err := startFakeDaemonAt(t, socket, pidFile); err != nil {
				return nil, err
			}
			return &exec.Cmd{Process: &os.Process{Pid: 1}}, nil
		},
	})

	const N = 8
	start := make(chan struct{}) // barrier so all N race the first mu acquire
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			errs[idx] = s.EnsureRunning()
		}(i)
	}
	close(start)
	wg.Wait()

	if n := atomic.LoadInt32(&spawnCalls); n != 2 {
		t.Fatalf("spawn called %d times, want exactly 2 (failed winner + one waiter-becomes-winner)", n)
	}
	errCount := 0
	for i, e := range errs {
		if e == nil {
			continue
		}
		errCount++
		if !strings.Contains(e.Error(), "spawn failed") {
			t.Errorf("caller %d got unexpected error %v, want the spawn-1 failure", i, e)
		}
	}
	if errCount != 1 {
		t.Errorf("%d callers returned errors, want exactly 1 (the failed first winner)", errCount)
	}
}

// TestStopStalePID verifies Stop cleans up a stale PID file without error.
func TestStopStalePID(t *testing.T) {
	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "daemon.sock")
	pidFile := filepath.Join(dir, "daemon.pid")
	// A pid that is definitely not alive.
	if err := os.WriteFile(pidFile, []byte("999999\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewSupervisor(SupervisorConfig{SocketPath: socket, PIDFile: pidFile, Stderr: io.Discard})
	msg, err := s.Stop()
	if err != nil {
		t.Fatalf("stale pid should not error: %v", err)
	}
	if msg != "" {
		t.Errorf("msg = %q, want empty", msg)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("pidfile should be removed")
	}
}

// TestStopLiveProcess covers Supervisor.Stop against a real (child) process:
// the child gets SIGTERM, Stop waits for exit, and reports "daemon stopped".
func TestStopLiveProcess(t *testing.T) {
	if os.Getenv("PHONEFAST_TEST_SLEEP_HELPER") == "1" {
		time.Sleep(30 * time.Second) // killed by SIGTERM from Stop()
		return
	}

	dir := shortSocketDir(t)
	pidFile := filepath.Join(dir, "daemon.pid")
	socket := filepath.Join(dir, "daemon.sock")

	child := exec.Command(os.Args[0], "-test.run=TestStopLiveProcess")
	child.Env = append(os.Environ(), "PHONEFAST_TEST_SLEEP_HELPER=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	// Reap the child the moment it exits: until Wait() runs, a dead child is a
	// ZOMBIE, and signal(0)-based IsProcessAlive reports zombies as alive —
	// which would make Stop's exit-poll never see the child die.
	reaped := make(chan struct{})
	go func() {
		child.Wait()
		close(reaped)
	}()
	defer func() {
		child.Process.Kill()
		<-reaped
	}()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", child.Process.Pid)), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewSupervisor(SupervisorConfig{SocketPath: socket, PIDFile: pidFile, Stderr: io.Discard})
	msg, err := s.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if msg != "daemon stopped" {
		t.Errorf("msg = %q, want %q", msg, "daemon stopped")
	}
	if IsProcessAlive(child.Process.Pid) {
		t.Error("child process still alive after Stop")
	}
}

// TestRestartReusesPersistedEnv covers the env-file read-back in restartOnce:
// an explicit Spawn with extra env (e.g. `phonefast daemon --ocr-engine
// apple`) persists it, and a later auto-restart from a DIFFERENT CLI
// invocation (a fresh Supervisor with empty ExtraEnv) must reuse the
// persisted env instead of silently reverting to defaults.
func TestRestartReusesPersistedEnv(t *testing.T) {
	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "daemon.sock")
	pidFile := filepath.Join(dir, "daemon.pid")
	envFile := filepath.Join(dir, "daemon.env")

	// Supervisor A: explicit spawn carrying extra env.
	a := NewSupervisor(SupervisorConfig{
		SocketPath: socket, PIDFile: pidFile, EnvFile: envFile, Stderr: io.Discard,
		Spawn: func(extraEnv []string) (*exec.Cmd, error) {
			return &exec.Cmd{Process: &os.Process{Pid: 1}}, nil
		},
	})
	if _, err := a.Spawn([]string{"PHONEFAST_OCR_ENGINE=apple"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Supervisor B: a fresh CLI invocation (empty ExtraEnv) auto-restarts and
	// must pick up the persisted env.
	var gotEnv []string
	b := NewSupervisor(SupervisorConfig{
		SocketPath: socket, PIDFile: pidFile, EnvFile: envFile, Stderr: io.Discard,
		Poll: time.Millisecond, MaxWait: 500 * time.Millisecond,
		Spawn: func(extraEnv []string) (*exec.Cmd, error) {
			gotEnv = append([]string(nil), extraEnv...)
			if err := startFakeDaemonAt(t, socket, pidFile); err != nil {
				return nil, err
			}
			return &exec.Cmd{Process: &os.Process{Pid: 1}}, nil
		},
	})
	if err := b.EnsureRunning(); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if len(gotEnv) != 1 || gotEnv[0] != "PHONEFAST_OCR_ENGINE=apple" {
		t.Fatalf("spawn env = %v, want [PHONEFAST_OCR_ENGINE=apple] (persisted env)", gotEnv)
	}
}

// TestEnsureRunningUnresponsiveDaemonRestart covers the alive-but-dead-socket
// branch of restartOnce: the pidfile's process is alive but the socket never
// answers Ping, so the supervisor must print "Daemon unresponsive",
// force-stop the old process, and spawn a fresh daemon.
func TestEnsureRunningUnresponsiveDaemonRestart(t *testing.T) {
	if os.Getenv("PHONEFAST_TEST_SLEEP_HELPER") == "1" {
		time.Sleep(30 * time.Second) // killed by forceStop during the test
		return
	}

	dir := shortSocketDir(t)
	socket := filepath.Join(dir, "daemon.sock")
	pidFile := filepath.Join(dir, "daemon.pid")

	// Unresponsive fake daemon: the socket accepts then hangs up immediately,
	// so Ping fails fast (EOF) instead of burning the 30s probe timeout.
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// The pidfile points at a real (alive) child process.
	child := exec.Command(os.Args[0], "-test.run=TestEnsureRunningUnresponsiveDaemonRestart")
	child.Env = append(os.Environ(), "PHONEFAST_TEST_SLEEP_HELPER=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	// Reap the child the moment it exits so signal(0)-based IsProcessAlive
	// observes its death (an unreaped zombie still reads as alive).
	reaped := make(chan struct{})
	go func() {
		child.Wait()
		close(reaped)
	}()
	defer func() {
		child.Process.Kill()
		<-reaped
	}()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", child.Process.Pid)), 0644); err != nil {
		t.Fatal(err)
	}

	var stderrBuf syncBuffer
	spawnCalls := 0
	s := NewSupervisor(SupervisorConfig{
		SocketPath: socket, PIDFile: pidFile, Stderr: &stderrBuf,
		Poll: time.Millisecond, MaxWait: 500 * time.Millisecond,
		Spawn: func(extraEnv []string) (*exec.Cmd, error) {
			spawnCalls++
			if err := startFakeDaemonAt(t, socket, pidFile); err != nil {
				return nil, err
			}
			return &exec.Cmd{Process: &os.Process{Pid: 1}}, nil
		},
	})
	if err := s.EnsureRunning(); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if spawnCalls != 1 {
		t.Errorf("spawn calls = %d, want 1 (old daemon force-stopped, fresh one spawned)", spawnCalls)
	}
	if !strings.Contains(stderrBuf.String(), "Daemon unresponsive, restarting...") {
		t.Errorf("stderr = %q, want it to contain 'Daemon unresponsive, restarting...'", stderrBuf.String())
	}
	if IsProcessAlive(child.Process.Pid) {
		t.Error("unresponsive daemon process still alive — forceStop did not kill it")
	}
}

// syncBuffer is a goroutine-safe io.Writer for capturing Supervisor progress
// output (the supervisor may print from spawn-hook goroutines).
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// TestStopForceKillUnreachable covers Stop's force-kill branch: the daemon
// process is dead but never reaped (zombie), so signal(0)-based
// IsProcessAlive keeps reporting it alive through the full graceful-stop
// window, and Stop must escalate to "daemon killed" rather than hang or
// misreport "daemon stopped". Slow by construction (~10s): killDaemon waits
// its full 3s graceful window per call plus Stop's 5s exit poll.
func TestStopForceKillUnreachable(t *testing.T) {
	if os.Getenv("PHONEFAST_TEST_SLEEP_HELPER") == "1" {
		time.Sleep(60 * time.Second) // SIGTERM'd by Stop, then left unreaped
		return
	}

	dir := shortSocketDir(t)
	pidFile := filepath.Join(dir, "daemon.pid")
	socket := filepath.Join(dir, "daemon.sock")

	child := exec.Command(os.Args[0], "-test.run=TestStopForceKillUnreachable")
	child.Env = append(os.Environ(), "PHONEFAST_TEST_SLEEP_HELPER=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	// Deliberately do NOT reap until Stop has returned: the dead child stays
	// a zombie, which IsProcessAlive reports as alive — this drives Stop past
	// its graceful window into the force-kill branch.
	defer func() {
		child.Process.Kill()
		child.Wait()
	}()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", child.Process.Pid)), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewSupervisor(SupervisorConfig{SocketPath: socket, PIDFile: pidFile, Stderr: io.Discard})
	msg, err := s.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if msg != "daemon killed" {
		t.Errorf("msg = %q, want %q", msg, "daemon killed")
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("pidfile should be removed after force kill")
	}
}
