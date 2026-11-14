package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
)

// writeScript writes body to name in dir and returns its path.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// sourceForms are the ways to source a file at the prompt: alone on the
// line (`. f`, `source f`) and with more after it (`. f; true`). Each runs
// the file as the same kind of line.
func sourceForms(path string, args string) []string {
	return []string{
		". " + path + args,
		"source " + path + args,
		". " + path + args + "; true",
	}
}

// A sourced file runs as the line it is: a background job it starts
// outlives the line, as in bash.
func TestRunShellLine_SourcedBackgroundJobOutlivesTheLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i, line := range sourceForms(writeScript(t, dir, "bg.sh", `{ sleep 0.2; echo hi > "$OUT"; } &`+"\n"), "") {
		sh := &Shell{config: config.Default(), executor: executor.New()}
		out := filepath.Join(dir, "out"+string(rune('a'+i)))
		sh.executor.SetExportedEnv("OUT", out)
		captureStdout(t, sh, func() {
			if _, err := sh.runShellLine(context.Background(), line); err != nil {
				t.Fatal(err)
			}
		})
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(out); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("%q: the sourced file's background job was cut off with its line", line)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// A sourced file's status, its positional arguments and an exit in it are
// the line's, as in bash.
func TestRunShellLine_SourcedFileIsTheLine(t *testing.T) {
	dir := t.TempDir()
	st := writeScript(t, dir, "st.sh", "false\n")
	arg := writeScript(t, dir, "arg.sh", `echo "arg=$1"`+"\n")
	ex := writeScript(t, dir, "ex.sh", "exit 3\n")
	for _, form := range []string{". ", "source "} {
		sh := &Shell{config: config.Default(), executor: executor.New()}
		ctx := context.Background()
		captureStdout(t, sh, func() {
			if _, err := sh.runShellLine(ctx, form+st); err != nil {
				t.Fatal(err)
			}
		})
		if sh.last.exitCode != 1 {
			t.Errorf("%q ended %d, want 1 (the file's last status)", form+st, sh.last.exitCode)
		}
		out := captureStdout(t, sh, func() {
			if _, err := sh.runShellLine(ctx, form+arg+" X"); err != nil {
				t.Fatal(err)
			}
		})
		if out != "arg=X\n" {
			t.Errorf("%q printed %q, want %q", form+arg+" X", out, "arg=X\n")
		}
		var exit shellExit
		_, err := sh.runShellLine(ctx, form+ex)
		if !errors.As(err, &exit) || exit.status != 3 {
			t.Errorf("%q returned %v, want the shell to exit 3", form+ex, err)
		}
	}
}

// Ctrl+C during a sourced file that loops in-process ends the line with
// 130, as for any interrupted line.
func TestRunShellLine_CtrlCStopsASourcedFile(t *testing.T) {
	loop := writeScript(t, t.TempDir(), "loop.sh", `: >"$STARTED"; while :; do :; done`+"\n")
	for _, line := range sourceForms(loop, "") {
		if o := interruptedLine(t, line); o.exitCode != 130 {
			t.Errorf("%q ended %d, want 130", line, o.exitCode)
		}
	}
}
