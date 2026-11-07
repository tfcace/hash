package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startDir is a directory holding a binary that is no program (ENOEXEC
// with a NUL in its first line), a script with no shebang, and a script
// whose interpreter is not there.
func startDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{
		"bin":   "\x00\x01binary",
		"nosb":  "echo noshebang-ran \"$0\" \"$1\"\n",
		"badsb": "#!/no/such/interp\necho never\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runIn(t *testing.T, dir, line string) (res *Result, out, errOut string) {
	t.Helper()
	t.Chdir(dir) // the executor follows the shell's directory: it chdirs hash
	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	res, err := e.Execute(ctx, line, &stdout, &stderr)
	if err != nil {
		t.Fatalf("%q: %v", line, err)
	}
	return res, stdout.String(), stderr.String()
}

// A command that cannot start sets 126 with bash's message, and the line
// goes on (`./bin || fallback`), as in bash: it ended neither the line
// nor the shell.
func TestExecute_StartFailureIsStatus126(t *testing.T) {
	dir := startDir(t)
	tests := []struct{ line, out, stderr string }{
		{"./bin || echo fallback=$?", "fallback=126\n", "hash: ./bin: cannot execute binary file: Exec format error\n"},
		{"/bin/echo $(seq 1 600000) >/dev/null; echo st=$?", "st=126\n", "hash: /bin/echo: Argument list too long\n"},
		{"./badsb; echo st=$?", "st=126\n", "hash: ./badsb: /no/such/interp: bad interpreter: No such file or directory\n"},
	}
	for _, tc := range tests {
		res, out, errOut := runIn(t, dir, tc.line)
		if res.End != LineReturned || out != tc.out || errOut != tc.stderr || len(res.Refusals) != 1 {
			t.Errorf("%q: end %v, out %q, stderr %q, refusals %v; want LineReturned, %q, %q, one refusal",
				tc.line, res.End, out, errOut, res.Refusals, tc.out, tc.stderr)
		}
	}
}

// A file with no shebang runs as a sh script, as bash runs one (ENOEXEC),
// with $0 the command as typed.
func TestExecute_NoShebangRunsAsSh(t *testing.T) {
	dir := startDir(t)
	res, out, errOut := runIn(t, dir, "./nosb a; echo st=$?")
	if want := "noshebang-ran ./nosb a\nst=0\n"; out != want || errOut != "" || res.End != LineReturned {
		t.Fatalf("out %q, stderr %q, end %v; want %q", out, errOut, res.End, want)
	}
	res, out, _ = runIn(t, dir, "PATH="+dir+":$PATH nosb b")
	if !strings.HasPrefix(out, "noshebang-ran "+filepath.Join(dir, "nosb")+" b") || res.ExitCode != 0 {
		t.Fatalf("by PATH: out %q, status %d", out, res.ExitCode)
	}
}

// A command gets its name as typed for argv[0], as bash gives it, not the
// path hash found it at: `sh -c 'echo $0'` prints sh.
func TestExecute_CommandsArgv0IsItsName(t *testing.T) {
	res, out, _ := runIn(t, t.TempDir(), `sh -c 'echo $0'; /bin/sh -c 'echo $0'`)
	if out != "sh\n/bin/sh\n" || res.ExitCode != 0 {
		t.Fatalf("argv[0]s %q, want sh then /bin/sh", out)
	}
}

// A job's command starts as a foreground one does: through hash itself
// (jobexec), which keeps its argv[0], says why a command cannot start in
// bash's words with 126, and runs a file with no shebang as sh. It used to
// start through /bin/sh, which named the command by its path and said
// "cannot execute binary file" and then "Undefined error: 0", status 0.
func TestExecute_JobStartsAsTheForeground(t *testing.T) {
	dir := startDir(t)
	res, out, errOut := runIn(t, dir, `./bin & wait $!; echo "bin=$?"; ./nosb b & wait $!; echo "nosb=$?"; sh -c 'echo "argv0=$0"' & wait`)
	if want := "bin=126\nnoshebang-ran ./nosb b\nnosb=0\nargv0=sh\n"; out != want || res.ExitCode != 0 {
		t.Errorf("out %q, want %q", out, want)
	}
	if want := "hash: ./bin: cannot execute binary file: Exec format error\n"; errOut != want {
		t.Errorf("stderr %q, want %q", errOut, want)
	}
}

// With hash's own binary gone (an upgrade removed it), a job's command
// starts all the same, as it is.
func TestExecute_JobStartsWithoutHashsBinary(t *testing.T) {
	e := New()
	e.shellPath = filepath.Join(t.TempDir(), "gone")
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `sh -c 'echo ran' & wait`, &out, io.Discard); err != nil || out.String() != "ran\n" {
		t.Fatalf("out %q, err %v; want ran", out.String(), err)
	}
}

// exec replaces the shell only once its command started: then the shell's
// traps go and the line ends the shell with the command's status. One that
// cannot start leaves the interactive shell running, its traps kept, with
// 126 and bash's message; a script ends with 126 and runs no EXIT trap, as
// bash's.
func TestExecute_ExecEndsTheShellOnceItsCommandStarted(t *testing.T) {
	dir := startDir(t)
	t.Chdir(dir)
	tests := []struct {
		mode   terminalMode
		line   string
		end    LineEnd
		status int
		out    string
		trap   bool // the EXIT trap still runs
	}{
		{terminalInteractive, "exec ./bin; echo after=$?", LineReturned, 0, "after=126\n", true},
		{terminalInteractive, "exec ./badsb; echo after=$?", LineReturned, 0, "after=126\n", true},
		{terminalInteractive, "builtin exec ./bin; echo after=$?", LineReturned, 0, "after=126\n", true},
		{terminalInteractive, "exec sh -c 'exit 4'; echo after", LineExited, 4, "", false},
		{terminalInteractive, "exec -- sh -c 'exit 5'; echo after", LineExited, 5, "", false},
		{terminalInteractive, "exec ./nosb; echo after", LineExited, 0, "noshebang-ran ./nosb \n", false},
		{terminalPlain, "exec ./bin; echo after", LineExited, 126, "", false},
		{terminalPlain, "exec nosuch-hash-cmd; echo after", LineExited, 127, "", true},
		{terminalPlain, "exec sh -c 'exit 6'; echo after", LineExited, 6, "", false},
		{terminalPlain, "(exec sh -c 'exit 7'); echo after=$?", LineReturned, 0, "after=7\n", true},
		{terminalPlain, "exec 3>/dev/null; echo after", LineReturned, 0, "after\n", true},
	}
	for _, tc := range tests {
		e := New()
		e.terminal = tc.mode
		var out, errb bytes.Buffer
		if _, err := e.Execute(context.Background(), "trap 'echo bye' EXIT", &out, &errb); err != nil {
			t.Fatal(err)
		}
		res, err := e.Execute(context.Background(), tc.line, &out, &errb)
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		if res.End != tc.end || res.ExitCode != tc.status || out.String() != tc.out {
			t.Errorf("%q (mode %d): end %v, status %d, out %q, stderr %q; want end %v, status %d, out %q",
				tc.line, tc.mode, res.End, res.ExitCode, out.String(), errb.String(), tc.end, tc.status, tc.out)
		}
		var trapOut bytes.Buffer
		e.RunExitTrap(context.Background(), &trapOut, io.Discard)
		if ran := trapOut.String() == "bye\n"; ran != tc.trap {
			t.Errorf("%q (mode %d): EXIT trap ran %v, want %v", tc.line, tc.mode, ran, tc.trap)
		}
	}
}

// Commands run in a working directory that was deleted, as in bash: they
// inherit hash's own, which the deleted directory still is. A shell
// (a subshell) whose directory is not hash's and cannot be entered says so.
func TestExecute_DeletedWorkingDirectory(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(base, "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(base)
	e := New()
	var out, errb bytes.Buffer
	if _, err := e.Execute(context.Background(), "cd gone", &out, &errb); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ line, out string }{
		{"/bin/echo still-runs; echo st=$?", "still-runs\nst=0\n"},
		{"/bin/echo piped | cat; echo st=$?", "piped\nst=0\n"},
		{"/bin/echo job & wait $!; echo st=$?", "job\nst=0\n"},
	} {
		out.Reset()
		errb.Reset()
		res, err := e.Execute(context.Background(), tc.line, &out, &errb)
		if err != nil {
			t.Fatal(err)
		}
		if res.End != LineReturned || out.String() != tc.out || errb.Len() != 0 || len(res.Refusals) != 0 {
			t.Errorf("%q: end %v, out %q, stderr %q, refusals %v; want %q and no error", tc.line, res.End, out.String(), errb.String(), res.Refusals, tc.out)
		}
	}

	t.Chdir(base)
	e = New()
	out.Reset()
	errb.Reset()
	res, err := e.Execute(context.Background(), "mkdir d; (cd d && rmdir ../d && /bin/echo x); echo st=$?", &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	want := "hash: /bin/echo: cannot enter the working directory " + filepath.Join(base, "d") + ": No such file or directory\n"
	if out.String() != "st=126\n" || errb.String() != want || len(res.Refusals) != 1 {
		t.Errorf("subshell: out %q, stderr %q, refusals %v; want st=126, %q", out.String(), errb.String(), res.Refusals, want)
	}
}

// A directory the shell entered and deleted on the same line is hash's
// own at once, as bash's cd moves the shell: commands run in it on that
// line and the lines after. hash moved its own directory only as the line
// ended, from $PWD, which a deleted directory refused: every command
// failed with 126 from then on.
func TestExecute_DirectoryDeletedOnTheLineThatEnteredIt(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(base)
	e := New()
	var out, errb bytes.Buffer
	if _, err := e.Execute(context.Background(), "mkdir d; cd d; rmdir ../d; /bin/echo same-line; echo st=$?", &out, &errb); err != nil {
		t.Fatal(err)
	}
	if want := "same-line\nst=0\n"; out.String() != want || errb.Len() != 0 {
		t.Errorf("the line that deleted it: out %q, stderr %q; want %q", out.String(), errb.String(), want)
	}
	out.Reset()
	errb.Reset()
	if _, err := e.Execute(context.Background(), "/bin/echo next-line; echo st=$?", &out, &errb); err != nil {
		t.Fatal(err)
	}
	if want := "next-line\nst=0\n"; out.String() != want || errb.Len() != 0 {
		t.Errorf("the next line: out %q, stderr %q; want %q", out.String(), errb.String(), want)
	}
}

// pushd and popd, the interpreter's own, into a directory deleted on the
// same line: hash follows the shell there before the next command, as it
// follows cd, so commands run in it on that line and the next ones. hash
// moved only as the line ended, and a deleted directory refused it.
func TestExecute_PushdIntoADirectoryDeletedOnTheLine(t *testing.T) {
	for _, line := range []string{
		"mkdir d; pushd d >/dev/null; rmdir ../d",
		"mkdir d; pushd d >/dev/null; pushd .. >/dev/null; popd >/dev/null; rmdir ../d",
	} {
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Chdir(base)
		e := New()
		var out, errb bytes.Buffer
		if _, err := e.Execute(context.Background(), line+"; /bin/echo same-line; echo st=$?", &out, &errb); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Execute(context.Background(), "/bin/echo next-line; echo st=$?", &out, &errb); err != nil {
			t.Fatal(err)
		}
		if want := "same-line\nst=0\nnext-line\nst=0\n"; out.String() != want || errb.Len() != 0 {
			t.Errorf("%q: out %q, stderr %q; want %q", line, out.String(), errb.String(), want)
		}
	}
}

// Setting $PWD moves neither the shell nor hash: the shell's directory is
// the one cd went to, as in bash (`PWD=/usr; /bin/pwd` prints where the
// shell is). hash followed $PWD at each line's end and moved its own
// process there, so a later command whose shell was not there ran there.
func TestExecute_SettingPWDMovesNothing(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(base)
	e := New()
	var out bytes.Buffer
	for _, line := range []string{"PWD=/usr", "/bin/pwd"} {
		if _, err := e.Execute(context.Background(), line, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if wd, _ := os.Getwd(); wd != base || out.String() != base+"\n" {
		t.Errorf("after PWD=/usr: hash in %q, /bin/pwd %q; want both %q", wd, out.String(), base)
	}
}

// A job starts through hash's own executable only while it is the one
// hash started from: once an upgrade replaced it, the job's command starts
// as it is, never through a binary that may not start it as jobexec does.
func TestExecute_JobStartsWithoutAReplacedHash(t *testing.T) {
	self := filepath.Join(t.TempDir(), "hash")
	if err := os.WriteFile(self, []byte("#!/bin/sh\nexec \"$2\" \"${@:4}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := New()
	e.shellPath, e.self = self, binaryAt(self)
	if err := os.Remove(self); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(self, []byte("#!/bin/sh\necho replaced\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `sh -c 'echo ran' & wait`, &out, io.Discard); err != nil || out.String() != "ran\n" {
		t.Fatalf("out %q, err %v; want ran", out.String(), err)
	}
}
