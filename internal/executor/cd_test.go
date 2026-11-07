package executor

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// cdSandbox is a directory holding a/sub, a symlink lnk to a, a symlink
// deep to a/sub and a file f, symlinks resolved, with the process in it; the process's directory comes
// back when the test ends.
func cdSandbox(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "a", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "a"), filepath.Join(base, "lnk")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "a", "sub"), filepath.Join(base, "deep")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	return base
}

// cd takes every form of bash 5.2's cd, with its messages and statuses
// (checked against bash:5.2 in docker). The interpreter's cd took one
// directory only: `cd -P x`, `cd -L x` and `cd -- -` were "usage: cd
// [dir]", CDPATH was never searched, and its errors were not bash's.
func TestCd_TakesBashsForms(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, line   string
		out, errText string // B in them is the sandbox
		status       int
	}{
		{"dash", "cd a; cd /; cd -; pwd", "B/a\nB/a\n", "", 0},
		{"dash after --", "cd a; cd /; cd -- -; pwd", "B/a\nB/a\n", "", 0},
		{"no OLDPWD", "unset OLDPWD; cd -", "", "hash: cd: OLDPWD not set\n", 1},
		{"logical", "cd lnk; pwd; echo $PWD", "B/lnk\nB/lnk\n", "", 0},
		{"-P", "cd -P lnk; pwd; echo $PWD", "B/a\nB/a\n", "", 0},
		{"-L after -P", "cd -P -L lnk; pwd", "B/lnk\n", "", 0},
		{"-PL", "cd -PL lnk; pwd", "B/lnk\n", "", 0},
		{"-P -e", "cd -Pe lnk; pwd", "B/a\n", "", 0},
		{"-P resolves before ..", "cd deep; cd -P ..; pwd", "B/a\n", "", 0},
		{"-P resolves an operand's link before its ..", "cd -P deep/..; pwd", "B/a\n", "", 0},
		{"-L takes .. logically", "cd deep; cd ..; pwd", "B\n", "", 0},
		{"-L falls back to the physical path", "cd deep/../sub; pwd; echo $PWD", "B/a/sub\nB/a/sub\n", "", 0},
		{"-P missing", "cd -P deep/../nope", "", "hash: cd: deep/../nope: No such file or directory\n", 1},
		{"too many", "cd a b", "", "hash: cd: too many arguments\n", 1},
		{"invalid option", "cd -x a", "", "hash: cd: -x: invalid option\ncd: usage: cd [-L|[-P [-e]]] [dir]\n", 2},
		{"missing", "cd nope", "", "hash: cd: nope: No such file or directory\n", 1},
		{"file", "cd f", "", "hash: cd: f: Not a directory\n", 1},
		{"empty", `cd ""; pwd`, "B\n", "", 0},
		{"HOME", "HOME=B/a; cd; pwd", "B/a\n", "", 0},
		{"no HOME", "unset HOME; cd", "", "hash: cd: HOME not set\n", 1},
		{"empty HOME", `HOME=""; cd; pwd`, "B\n", "", 0},
		{"CDPATH", "CDPATH=B; cd /; cd a; pwd", "B/a\nB/a\n", "", 0},
		{"CDPATH -P", "CDPATH=B; cd /; cd -P lnk; pwd", "B/a\nB/a\n", "", 0},
		{"CDPATH -P resolves a hit's link before its ..", "CDPATH=B; cd /; cd -P deep/..; pwd", "B/a\nB/a\n", "", 0},
		{"CDPATH takes a hit's .. logically", "CDPATH=B; cd /; cd deep/..; pwd", "B\nB\n", "", 0},
		{"CDPATH hit through the physical path", "CDPATH=B; cd /; cd deep/../sub; pwd", "B/a/sub\nB/a/sub\n", "", 0},
		{"CDPATH empty entry is .", "CDPATH=:/; cd a; pwd", "B/a\n", "", 0},
		{"CDPATH skips ./", "CDPATH=B; cd /; cd ./a", "", "hash: cd: ./a: No such file or directory\n", 1},
		{"~user", "cd ~" + me.Username + "; pwd", me.HomeDir + "\n", "", 0},
		{"function cd gets the call", `cd() { echo "f $*"; }; cd -P x`, "f -P x\n", "", 0},
		{"builtin cd past the function", `cd() { echo f; }; builtin cd -P lnk; pwd`, "B/a\n", "", 0},
		{"command cd past the function", `cd() { echo f; }; command cd -- a; pwd`, "B/a\n", "", 0},
		{"zoxide's call", `\builtin cd -- a; pwd`, "B/a\n", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := cdSandbox(t)
			e := New()
			var stdout, stderr bytes.Buffer
			line := strings.ReplaceAll(tt.line, "B", base)
			res, err := e.Execute(context.Background(), line, &stdout, &stderr)
			if err != nil {
				t.Fatal(err)
			}
			wantOut := strings.ReplaceAll(tt.out, "B", base)
			wantErr := strings.ReplaceAll(tt.errText, "B", base)
			if stdout.String() != wantOut || stderr.String() != wantErr || res.ExitCode != tt.status {
				t.Errorf("%s:\nstdout %q, want %q\nstderr %q, want %q\nstatus %d, want %d",
					line, stdout.String(), wantOut, stderr.String(), wantErr, res.ExitCode, tt.status)
			}
		})
	}
}

// PWD and OLDPWD are exported once cd sets them, as bash exports them, to
// the commands the shell runs and to hash's own environment (the prompt
// reads $PWD): the interpreter's cd set them unexported.
func TestCd_ExportsPWDAndOLDPWD(t *testing.T) {
	base := cdSandbox(t)
	t.Setenv("PWD", base)
	t.Setenv("OLDPWD", "")
	os.Unsetenv("OLDPWD")
	e := New()
	var stdout, stderr bytes.Buffer
	if _, err := e.Execute(context.Background(), "cd a; /usr/bin/printenv PWD; /usr/bin/printenv OLDPWD", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(base, "a")
	if want := a + "\n" + base + "\n"; stdout.String() != want {
		t.Errorf("a command saw PWD and OLDPWD %q, want %q (stderr %q)", stdout.String(), want, stderr.String())
	}
	if os.Getenv("PWD") != a || os.Getenv("OLDPWD") != base {
		t.Errorf("hash's PWD %q, OLDPWD %q; want %q and %q", os.Getenv("PWD"), os.Getenv("OLDPWD"), a, base)
	}
}
