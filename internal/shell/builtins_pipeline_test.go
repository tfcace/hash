package shell

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/history"
)

func newHistoryShell(t *testing.T, commands ...string) *Shell {
	t.Helper()
	store, err := history.NewStore(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, c := range commands {
		if _, err := store.Add(history.Command{Command: c, Timestamp: time.Now()}); err != nil {
			t.Fatalf("Add(%q): %v", c, err)
		}
	}
	sh := &Shell{config: config.Default(), executor: executor.New(), history: store}
	sh.registerExecutorBuiltins()
	return sh
}

// captureStdout runs fn with os.Stdout redirected and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func TestExecuteRegularCommand_HistoryPipesThroughGrep(t *testing.T) {
	sh := newHistoryShell(t, "ls -la", "jj new", "git status", "jj log")
	out := captureStdout(t, func() {
		if err := sh.executeRegularCommand(context.Background(), "history | grep jj"); err != nil {
			t.Fatalf("executeRegularCommand: %v", err)
		}
	})
	if !strings.Contains(out, "jj new") || !strings.Contains(out, "jj log") {
		t.Errorf("output %q should contain the jj commands", out)
	}
	if strings.Contains(out, "git status") || strings.Contains(out, "ls -la") {
		t.Errorf("output %q should have been filtered by grep", out)
	}
}

func TestExecuteRegularCommand_HistoryListsRecent(t *testing.T) {
	sh := newHistoryShell(t, "ls -la", "jj new")
	out := captureStdout(t, func() {
		if err := sh.executeRegularCommand(context.Background(), "history"); err != nil {
			t.Fatalf("executeRegularCommand: %v", err)
		}
	})
	if !strings.Contains(out, "jj new") || !strings.Contains(out, "ls -la") {
		t.Errorf("output %q should list both commands", out)
	}
}

func TestExecuteBuiltin_FallsThroughForNonSimpleLines(t *testing.T) {
	sh := newHistoryShell(t)
	for _, line := range []string{"history | grep jj", "cd /tmp && pwd", "history > out.txt", "cd $HOME"} {
		handled, err := sh.executeBuiltin(context.Background(), line)
		if handled || err != nil {
			t.Errorf("executeBuiltin(%q) = handled %v, err %v; want fall-through to the interpreter", line, handled, err)
		}
	}
}

func TestExecuteRegularCommand_CdWithAndRunsBothCommands(t *testing.T) {
	sh := newHistoryShell(t)
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	out := captureStdout(t, func() {
		if err := sh.executeRegularCommand(context.Background(), "cd "+dir+" && echo moved"); err != nil {
			t.Fatalf("executeRegularCommand: %v", err)
		}
	})
	if !strings.Contains(out, "moved") {
		t.Errorf("second command after && did not run; output %q", out)
	}
	cwd, _ := os.Getwd()
	cwdR, _ := filepath.EvalSymlinks(cwd)
	dirR, _ := filepath.EvalSymlinks(dir)
	if cwdR != dirR {
		t.Errorf("cwd = %q, want %q", cwd, dir)
	}
}
