package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// How a line ended is worked out once (lineEnd): the exit builtin and
// errexit end the shell, an expansion error ends only the line, as in
// bash, whose interactive shell prints the error and goes on and whose
// script exits with 127.
func TestExecute_LineEnd(t *testing.T) {
	tests := []struct {
		line   string
		end    LineEnd
		status int
		stderr string
	}{
		{"true", LineReturned, 0, ""},
		{"false", LineReturned, 1, ""},
		{"( exit 2 )", LineReturned, 2, ""},
		{"exit 1 2; echo after", LineReturned, 0, "exit cannot take multiple arguments"},
		{"exit 3", LineExited, 3, ""},
		{"f() { exit 4; }; f", LineExited, 4, ""},
		{"set -e; false", LineExited, 1, ""},
		{"echo ${nope:?unset here}; echo after", LineFailed, 127, "nope: unset here"},
		{"set -u; echo $nope2; echo after", LineFailed, 127, "nope2"},
		{"x=$(echo ${nope:?inner}); echo after=$?", LineReturned, 0, "nope: inner"},
	}
	for _, tc := range tests {
		e := New()
		var out, errb bytes.Buffer
		res, err := e.Execute(context.Background(), tc.line, &out, &errb)
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		if res.End != tc.end || res.ExitCode != tc.status || !strings.Contains(errb.String(), tc.stderr) {
			t.Errorf("%q: end %v, status %d, stderr %q; want end %v, status %d, stderr with %q",
				tc.line, res.End, res.ExitCode, errb.String(), tc.end, tc.status, tc.stderr)
		}
		if tc.end == LineFailed && strings.Contains(out.String(), "after") {
			t.Errorf("%q: the line went on after its fatal error: %q", tc.line, out.String())
		}
	}
}

// The interactive shell's expansion error sets 1, as bash 5.2's (a
// script's sets 127 as it ends it).
func TestExecute_InteractiveExpansionErrorStatus(t *testing.T) {
	e := New()
	e.terminal = terminalInteractive
	var out, errb bytes.Buffer
	res, err := e.Execute(context.Background(), "echo ${nope:?msg}; echo after", &out, &errb)
	if err != nil || res.End != LineFailed || res.ExitCode != 1 || out.Len() != 0 {
		t.Fatalf("end %v, status %d, out %q, err %v; want LineFailed, 1, nothing", res.End, res.ExitCode, out.String(), err)
	}
	res, _ = e.Execute(context.Background(), "echo $?", &out, &errb)
	if out.String() != "1\n" || res.End != LineReturned {
		t.Fatalf("$? after the failed line = %q, want 1", out.String())
	}
}

// An exec whose command is not there fails as a command that is not
// there, and the interactive shell's line goes on, as bash's does; a
// script ends, as bash's (with no execfail).
func TestExecute_ExecOfAMissingCommand(t *testing.T) {
	e := New()
	e.terminal = terminalInteractive
	var out, errb bytes.Buffer
	for _, line := range []string{"exec nosuch-hash-cmd; echo after=$?", "builtin exec nosuch-hash-cmd; echo after=$?"} {
		out.Reset()
		res, err := e.Execute(context.Background(), line, &out, &errb)
		if err != nil || res.End != LineReturned || out.String() != "after=127\n" || len(res.Refusals) != 1 {
			t.Fatalf("interactive %q: end %v, out %q, stderr %q, refusals %v, err %v; want the line to go on", line, res.End, out.String(), errb.String(), res.Refusals, err)
		}
	}
	e = New()
	out.Reset()
	res, _ := e.Execute(context.Background(), "exec nosuch-hash-cmd; echo after=$?", &out, &errb)
	if res.End == LineReturned || res.ExitCode != 127 || out.Len() != 0 {
		t.Fatalf("script: end %v, status %d, out %q; want the script to end with 127", res.End, res.ExitCode, out.String())
	}
}

// A bash pipeline's last command runs in a subshell, as in bash without
// lastpipe: an exit there ends the pipeline, not the shell, and a read or
// cd there leaves the shell as it was. zsh runs it in the shell itself.
func TestExecute_PipelineTailRunsInASubshell(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	tests := []struct {
		dialect, line string
		end           LineEnd
		status        int
		out           string
	}{
		{"bash", "echo hi | exit 3; echo after $?", LineReturned, 0, "after 3\n"},
		{"bash", "printf 'a\\n' | while read l; do exit 5; done; echo after $?", LineReturned, 0, "after 5\n"},
		{"bash", "echo x | read v; echo v=$v", LineReturned, 0, "v=\n"},
		{"bash", "echo | cd /; pwd", LineReturned, 0, dir + "\n"},
		{"bash", "true | true | exit 4; echo after $?", LineReturned, 0, "after 4\n"},
		{"bash", "set -o pipefail; exit 2 | true; echo after $?", LineReturned, 0, "after 2\n"},
		{"zsh", "echo x | read v; echo v=$v", LineReturned, 0, "v=x\n"},
		{"zsh", "echo hi | exit 3; echo after $?", LineExited, 3, ""},
	}
	for _, tc := range tests {
		for _, mode := range []terminalMode{terminalPlain, terminalInteractive} {
			e := New()
			e.terminal = mode
			if err := e.SetDialect(tc.dialect); err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			res, err := e.Execute(context.Background(), tc.line, &out, &errb)
			if err != nil {
				t.Fatalf("%s %q: %v", tc.dialect, tc.line, err)
			}
			if res.End != tc.end || res.ExitCode != tc.status || out.String() != tc.out {
				t.Errorf("%s %q (mode %d): end %v, status %d, out %q, stderr %q; want end %v, status %d, out %q",
					tc.dialect, tc.line, mode, res.End, res.ExitCode, out.String(), errb.String(), tc.end, tc.status, tc.out)
			}
		}
	}
}

// An exit in an ERR trap ends the shell, as in bash (a script and bash -i
// alike): the interpreter runs its trap callbacks keeping the status of
// the command that failed, so the exit took no effect and the line went on.
func TestExecute_ExitInAnErrTrapEndsTheShell(t *testing.T) {
	tests := []struct {
		line   string
		status int
		out    string
	}{
		{"trap 'echo trapped; exit 9' ERR; false; echo after", 9, "trapped\n"},
		{"trap exit ERR; (exit 3); echo after", 3, ""},
		{"f() { exit 8; }; trap f ERR; false; echo after", 8, ""},
		{"trap 'exit 5' ERR; for i in 1 2; do false; echo after; done", 5, ""},
	}
	for _, tc := range tests {
		for _, mode := range []terminalMode{terminalPlain, terminalInteractive} {
			e := New()
			e.terminal = mode
			var out, errb bytes.Buffer
			res, err := e.Execute(context.Background(), tc.line, &out, &errb)
			if err != nil {
				t.Fatalf("%q: %v", tc.line, err)
			}
			if res.End != LineExited || res.ExitCode != tc.status || out.String() != tc.out {
				t.Errorf("%q (mode %d): end %v, status %d, out %q, stderr %q; want LineExited, %d, %q",
					tc.line, mode, res.End, res.ExitCode, out.String(), errb.String(), tc.status, tc.out)
			}
		}
	}
}

// exit with a status that is no number says so and exits with 2, as bash
// (interactive or not, the shell or a subshell); the interpreter's exit
// refused it and went on.
func TestExecute_ExitWithANonNumericStatus(t *testing.T) {
	for _, mode := range []terminalMode{terminalPlain, terminalInteractive} {
		e := New()
		e.terminal = mode
		var out, errb bytes.Buffer
		res, err := e.Execute(context.Background(), "(exit abc; echo in); echo sub=$?; exit abc; echo after", &out, &errb)
		if err != nil {
			t.Fatal(err)
		}
		want := "hash: exit: abc: numeric argument required\n"
		if res.End != LineExited || res.ExitCode != 2 || out.String() != "sub=2\n" || errb.String() != want+want {
			t.Errorf("mode %d: end %v, status %d, out %q, stderr %q; want LineExited, 2, %q, %q twice",
				mode, res.End, res.ExitCode, out.String(), errb.String(), "sub=2\n", want)
		}
	}
}

// A return outside a function or a sourced file fails with 2 and bash's
// message, and the line goes on, as in bash (the interpreter's sets 1).
// Inside a function or a sourced file it returns.
func TestExecute_ReturnOutsideAFunction(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.sh"), []byte("return 6\necho never\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ line, out, stderr string }{
		{"return; echo after $?", "after 2\n", "hash: return: can only `return' from a function or sourced script\n"},
		{"return 3; echo after $?", "after 2\n", "hash: return: can only `return' from a function or sourced script\n"},
		{"f() { return 4; }; f; echo f=$?", "f=4\n", ""},
		{". " + shellQuote(filepath.Join(dir, "lib.sh")) + "; echo src=$?", "src=6\n", ""},
	}
	for _, tc := range tests {
		e := New()
		var out, errb bytes.Buffer
		res, err := e.Execute(context.Background(), tc.line, &out, &errb)
		if err != nil {
			t.Fatal(err)
		}
		if res.End != LineReturned || out.String() != tc.out || errb.String() != tc.stderr {
			t.Errorf("%q: end %v, out %q, stderr %q; want LineReturned, %q, %q", tc.line, res.End, out.String(), errb.String(), tc.out, tc.stderr)
		}
	}
}
