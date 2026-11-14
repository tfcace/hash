package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// signalHash starts hash with args and its stdin read from a fifo that is
// held open with nothing (or first) written to it, sends sig once hash is
// waiting, and returns what hash printed, how it ended and how long it
// took to end after the signal. Everything runs under hard deadlines.
func signalHash(t *testing.T, sig syscall.Signal, first string, args ...string) (stdout, stderr string, state *os.ProcessState, took time.Duration) {
	t.Helper()
	fifo := filepath.Join(t.TempDir(), "in")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	// O_RDWR keeps the fifo open for writing without blocking the open.
	keep, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer keep.Close()
	if first != "" {
		if _, err := keep.WriteString(first); err != nil {
			t.Fatal(err)
		}
	}
	in, err := os.Open(fifo)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, hashBinary(t), args...)
	cmd.WaitDelay = time.Second
	cmd.Env = isolatedEnv(t.TempDir())
	cmd.Stdin = in
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond) // hash is waiting on its stdin
	start := time.Now()
	_ = cmd.Process.Signal(sig)
	_ = cmd.Wait()
	took = time.Since(start)
	if ctx.Err() != nil {
		t.Fatalf("hash %q did not end after %v", args, sig)
	}
	return out.String(), errb.String(), cmd.ProcessState, took
}

// A fatal signal ends a script at once wherever it waits on stdin: in the
// read builtin, or for the next line of a script read from stdin. The
// script's own goroutine runs the EXIT trap; it used to run on the signal's
// goroutine, which waited for the read to get data (and raced the script's
// status, which -race reports).
func TestLaunch_SignalEndsAScriptWaitingOnStdin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, first string
		args        []string
	}{
		{"read in -c", "", []string{"-c", `trap "echo bye" EXIT; read x; echo after`}},
		{"next stdin line", "trap 'echo bye' EXIT\n", nil},
		{"read in a stdin script", "trap 'echo bye' EXIT\nread x\n", nil},
	}
	for _, tc := range cases {
		stdout, stderr, _, took := signalHash(t, syscall.SIGTERM, tc.first, tc.args...)
		if stdout != "bye\n" || took > 2*time.Second {
			t.Errorf("%s: stdout %q, ended %v after SIGTERM; want bye at once", tc.name, stdout, took.Round(time.Millisecond))
		}
		if strings.Contains(stderr, "DATA RACE") {
			t.Errorf("%s: race:\n%s", tc.name, stderr)
		}
	}
}

// A producer can stop between bytes, not just between lines. The partial
// command must not run, and the signal must still reach the EXIT trap.
func TestLaunch_SignalEndsAPartialStdinLine(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			stdout, stderr, state, took := signalHash(t, sig, "trap 'echo bye' EXIT\necho unfinished")
			if stdout != "bye\n" || took > 2*time.Second {
				t.Errorf("stdout %q, ended %v after %v; want only bye at once", stdout, took.Round(time.Millisecond), sig)
			}
			if status, ok := state.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != sig {
				t.Errorf("ended %v, want death by %v", state, sig)
			}
			if strings.Contains(stderr, "DATA RACE") {
				t.Errorf("race:\n%s", stderr)
			}
		})
	}
}
