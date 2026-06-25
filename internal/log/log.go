// Package log provides asynchronous file logging with caller context.
// A background goroutine drains a buffered channel and writes to the log file,
// keeping log I/O off the hot path.
package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Logger writes timestamped log entries to a file via a background goroutine.
// Write is non-blocking as long as the internal buffer isn't full.
type Logger struct {
	ch     chan string
	file   *os.File
	wg     sync.WaitGroup
	closed bool
	mu     sync.Mutex
}

// Config holds logger creation settings.
type Config struct {
	// Path to the log file. Created if it doesn't exist. Defaults to
	// /tmp/phonefast-<uid>.log.
	Path string

	// BufferSize is the number of buffered log entries before Write blocks.
	// Defaults to 1024.
	BufferSize int
}

// New creates a logger and starts its background writer goroutine.
// The caller must call Close() to flush and stop the writer.
func New(cfg Config) (*Logger, error) {
	if cfg.Path == "" {
		cfg.Path = fmt.Sprintf("/tmp/phonefast-%d.log", os.Getuid())
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 1024
	}

	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir %s: %w", dir, err)
	}

	file, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file %s: %w", cfg.Path, err)
	}

	l := &Logger{
		ch:   make(chan string, cfg.BufferSize),
		file: file,
	}

	l.wg.Add(1)
	go l.drain()

	return l, nil
}

// Write enqueues a log entry. Format is like fmt.Sprintf.
// Caller info (file:line, function name) is captured here, before the message
// is sent to the background goroutine.
// Non-blocking as long as the internal buffer has room; blocks briefly if full.
//
// The send on l.ch is performed under l.mu so it cannot race with Close()'s
// close(l.ch): once Close sets l.closed under the same lock, no concurrent
// Write can reach the channel send. This is the only correct way to guard a
// channel send against a concurrent close — the previous closed-check-then-send
// had a TOCTOU window that panicked under concurrent shutdown.
func (l *Logger) Write(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	entry := l.formatEntry(msg, 3) // skip: Write → formatEntry → caller

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}

	select {
	case l.ch <- entry:
	default:
		// Channel full — write directly to file to avoid losing the entry.
		fmt.Fprintf(os.Stderr, "[log] buffer full, writing synchronously\n")
		// Re-format with correct caller skip for the synchronous path
		// (one extra frame: the default case itself)
		l.file.WriteString(l.formatEntry(msg, 4))
	}
}

// Close flushes remaining entries, closes the file, and stops the writer.
func (l *Logger) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.ch)
	l.mu.Unlock()

	// Wait for drain to consume the remaining buffered entries. Done outside
	// the lock so drain isn't blocked on l.mu.
	l.wg.Wait()
	return l.file.Close()
}

// drain runs in a background goroutine, reading pre-formatted entries from the
// channel and writing them to the log file.
func (l *Logger) drain() {
	defer l.wg.Done()
	for entry := range l.ch {
		l.file.WriteString(entry)
	}
}

// formatEntry creates a complete log line with timestamp, caller info, and message.
func (l *Logger) formatEntry(msg string, callerSkip int) string {
	now := time.Now().Format("2006-01-02 15:04:05.000")
	caller := captureCaller(callerSkip)
	return fmt.Sprintf("%s [%s] %s\n", now, caller, msg)
}

// writeEntry is kept for synchronous fallback writes (buffer full case).
func (l *Logger) writeEntry(w io.Writer, msg string, callerSkip int) {
	w.Write([]byte(l.formatEntry(msg, callerSkip)))
}

// captureCaller returns a string like "daemon/daemon.go:147 Start()".
func captureCaller(skip int) string {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "???"
	}

	// Trim to last two path components, e.g. "daemon/daemon.go"
	shortFile := file
	for i := 0; i < 2; i++ {
		if idx := strings.LastIndex(shortFile, "/"); idx >= 0 {
			shortFile = shortFile[idx+1:]
		}
	}

	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return fmt.Sprintf("%s:%d", shortFile, line)
	}

	funcName := fn.Name()
	// Strip package path, keep Type.Method or funcName
	if idx := strings.LastIndex(funcName, "."); idx >= 0 {
		funcName = funcName[idx+1:]
	}

	return fmt.Sprintf("%s:%d %s()", shortFile, line, funcName)
}

// ── Default logger (package-level convenience) ──

var defaultLogger *Logger
var defaultOnce sync.Once

// Default returns a shared package-level logger writing to
// /tmp/phonefast-<uid>.log. Created once on first use.
func Default() *Logger {
	defaultOnce.Do(func() {
		var err error
		defaultLogger, err = New(Config{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[phonefast] failed to create default logger: %v\n", err)
			// Fall back to no-op
			defaultLogger, _ = New(Config{Path: os.DevNull})
		}
	})
	return defaultLogger
}

// CloseDefault flushes and closes the default logger.
func CloseDefault() {
	if defaultLogger != nil {
		defaultLogger.Close()
	}
}
