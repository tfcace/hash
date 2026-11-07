package shell

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// cdForHook moves the process to a new directory, as a cd line does, so
// the chpwd hooks run after it.
func cdForHook(t *testing.T, sh *Shell) {
	t.Helper()
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

// returnsWithin runs fn, failing the test when it has not returned in d.
func returnsWithin(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("still running after %v", d)
	}
}

// A chpwd hook's output is the user's to see, as a hook's in zsh: it ran
// with no stdout, so what it printed was lost.
func TestChpwdHook_PrintsToTheTerminal(t *testing.T) {
	sh := newShellForAgentCommand(t)
	sh.config.Shell.Hooks.Chpwd = []string{"echo hooked in $PWD"}
	cdForHook(t, sh)
	out := captureStdout(t, sh, func() { sh.runChpwdHook(context.Background()) })
	if !strings.Contains(out, "hooked in") {
		t.Errorf("hook printed %q, want its echo", out)
	}
}

// Ctrl+C interrupts a chpwd hook as it does a line: an endless hook hung
// the shell.
func TestChpwdHook_CtrlCInterruptsIt(t *testing.T) {
	sh := newShellForAgentCommand(t)
	sh.config.Shell.Hooks.Chpwd = []string{"while :; do :; done"}
	cdForHook(t, sh)
	go func() {
		time.Sleep(200 * time.Millisecond)
		sh.interrupts.fire()
	}()
	captureStdout(t, sh, func() {
		returnsWithin(t, 5*time.Second, func() { sh.runChpwdHook(context.Background()) })
	})
}

// Ctrl+C interrupts an init command as it does a line.
func TestInitCommands_CtrlCInterruptsOne(t *testing.T) {
	sh := newShellForAgentCommand(t)
	sh.config.Shell.InitCommands = []string{"while :; do :; done", "echo next"}
	go func() {
		time.Sleep(200 * time.Millisecond)
		sh.interrupts.fire()
	}()
	out := captureStdout(t, sh, func() {
		returnsWithin(t, 5*time.Second, func() {
			if err := sh.runInitCommands(context.Background()); err != nil {
				t.Error(err)
			}
		})
	})
	if !strings.Contains(out, "next") {
		t.Errorf("init commands printed %q, want the one after the interrupted one to run", out)
	}
}
