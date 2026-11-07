package shell

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cdShell is a shell in a fresh directory holding a and b, symlinks
// resolved; the process's directory comes back when the test ends.
func cdShell(t *testing.T) (*Shell, string) {
	t.Helper()
	sh := newShellForAgentCommand(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	if _, err := sh.executor.Execute(context.Background(), "cd '"+base+"'", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	return sh, base
}

// cd typed alone on a line is the shell's cd, as in any other line: hash
// ran its own, which took `-` for a directory named -.
func TestCdLine_DashGoesBack(t *testing.T) {
	sh, base := cdShell(t)
	out := captureStdout(t, sh, func() {
		for _, line := range []string{"cd a", "cd ../b", "cd -"} {
			if err := sh.handleLine(context.Background(), line); err != nil {
				t.Fatal(err)
			}
		}
	})
	cwd, _ := os.Getwd()
	if cwd != filepath.Join(base, "a") || sh.last.exitCode != 0 {
		t.Fatalf("after cd -: in %s with status %d, want %s and 0 (stderr %q)", cwd, sh.last.exitCode, filepath.Join(base, "a"), sh.last.stderr)
	}
	if !strings.Contains(out, filepath.Join(base, "a")+"\n") {
		t.Errorf("cd - printed %q, want the directory it went to", out)
	}
	if got := os.Getenv("PWD"); got != cwd {
		t.Errorf("$PWD in the environment = %q, want %q", got, cwd)
	}
}

// Its errors are bash's cd's, and it changes nothing.
func TestCdLine_ErrorsAreBashs(t *testing.T) {
	sh, base := cdShell(t)
	for line, want := range map[string]string{
		"cd a b":   "hash: cd: too many arguments",
		"cd nope":  "hash: cd: nope: No such file or directory",
		"cd -x":    "hash: cd: -x: invalid option",
		"cd ~nope": "hash: cd: ~nope: No such file or directory",
	} {
		captureStdout(t, sh, func() { _ = sh.handleLine(context.Background(), line) })
		if !strings.Contains(sh.last.stderr, want) || sh.last.exitCode == 0 {
			t.Errorf("%s: stderr %q, status %d; want %q and a failure", line, sh.last.stderr, sh.last.exitCode, want)
		}
		if cwd, _ := os.Getwd(); cwd != base {
			t.Errorf("%s moved the shell to %s", line, cwd)
		}
	}
}

// With cd listed in disable_builtins (setup-zoxide), a cd function or
// alias takes the call, typed alone or not.
func TestCdLine_DisabledCdGoesToTheFunction(t *testing.T) {
	sh, _ := cdShell(t)
	sh.config.Shell.DisableBuiltins = []string{"cd"}
	out := captureStdout(t, sh, func() {
		_ = sh.handleLine(context.Background(), `cd() { echo "function got $*"; }`)
		_ = sh.handleLine(context.Background(), "cd -P a")
	})
	if !strings.Contains(out, "function got -P a") {
		t.Errorf("printed %q, want the function to take cd -P a", out)
	}
}
