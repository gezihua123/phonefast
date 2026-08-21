package adb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeADB writes an executable `platform-tools/adb` script into a temp dir
// and points ANDROID_HOME at it (findADB checks ANDROID_HOME before PATH, so
// the real adb is never used). The script ignores its arguments and runs the
// given body.
func fakeADB(t *testing.T, script string) {
	t.Helper()
	platformTools := filepath.Join(t.TempDir(), "platform-tools")
	if err := os.MkdirAll(platformTools, 0o755); err != nil {
		t.Fatal(err)
	}
	adbPath := filepath.Join(platformTools, "adb")
	if err := os.WriteFile(adbPath, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANDROID_HOME", filepath.Dir(platformTools))
}

func TestADBShellReturnsTrimmedOutput(t *testing.T) {
	fakeADB(t, `echo hello`)
	out, err := ADBShell("serial1", "getprop", "ro.build.version.sdk")
	if err != nil {
		t.Fatalf("ADBShell: %v", err)
	}
	if out != "hello" {
		t.Errorf("output = %q, want %q", out, "hello")
	}
}

func TestADBShellTimeoutKillsHungADB(t *testing.T) {
	// exec so the killed process IS the sleeper: a plain `sleep 5` leaves the
	// sleep as a grandchild holding the stdout pipe open, which delays
	// cmd.Run()'s pipe drain past the context kill. The real adb client is a
	// single process, so killing it closes the pipe immediately.
	fakeADB(t, `exec sleep 5`)
	start := time.Now()
	_, err := ADBShellTimeout("serial1", 300*time.Millisecond, "am", "broadcast")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout took %v, want well under the fake adb's 5s sleep", elapsed)
	}
	// Callers (e.g. the broadcast path) must be able to distinguish a hang
	// from a command failure — a hung adb may have already delivered the
	// command, so the two cases have different retry semantics.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error %v must wrap context.DeadlineExceeded", err)
	}
}

func TestADBShellPropagatesCommandFailure(t *testing.T) {
	fakeADB(t, `echo boom >&2; exit 1`)
	_, err := ADBShell("serial1", "ime", "set", "x")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("plain command failure %v must not look like a timeout", err)
	}
}
