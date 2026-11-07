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

	"github.com/tfcace/hash/internal/programstatus"
)

// A startup file runs as a sourced file does: a return at its top level
// ends it, with its status, and is no error; one in a function returns
// from the function. The file's functions, aliases and variables stay.
func TestExecuteStartup_ReturnEndsTheFile(t *testing.T) {
	for _, tt := range []struct {
		name, text, out string
		code            int
	}{
		{"top level", "echo a\nreturn\necho b\n", "a\n", 0},
		{"status", "echo a\nreturn 3\necho b\n", "a\n", 3},
		{"in a case", "case x in\n  y) ;;\n  *) return;;\nesac\necho b\n", "", 0},
		{"in a function", "f() { return 4; echo no; }\nf\necho \"f=$?\"\n", "f=4\n", 0},
		{"none", "x=1\necho \"$x\"\n", "1\n", 0},
	} {
		e := New()
		var out, errOut bytes.Buffer
		res, err := e.ExecuteStartup(context.Background(), "", "", tt.text, &out, &errOut)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if out.String() != tt.out || errOut.String() != "" || res.ExitCode != tt.code {
			t.Errorf("%s: stdout %q stderr %q status %d, want %q, nothing, %d", tt.name, out.String(), errOut.String(), res.ExitCode, tt.out, tt.code)
		}
	}

	e := New()
	var out, errOut bytes.Buffer
	if _, err := e.ExecuteStartup(context.Background(), "", "", "f() { echo f; }\nalias g='echo g'\nv=1\nreturn\n", &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), "f; g; echo \"$v\"; source() { echo mine; }", &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "f\ng\n1\n" {
		t.Errorf("after the file: %q (stderr %q), want its function, alias and variable", out.String(), errOut.String())
	}
	out.Reset()
	if _, err := e.ExecuteStartup(context.Background(), "", "", "echo ran\nreturn\n", &out, &errOut); err != nil || out.String() != "ran\n" {
		t.Errorf("with a function named source: %q %v (stderr %q)", out.String(), err, errOut.String())
	}
}

// A startup file's syntax error is the file's error, as before.
func TestExecuteStartup_SyntaxErrorIsTheFiles(t *testing.T) {
	var out, errOut bytes.Buffer
	if _, err := New().ExecuteStartup(context.Background(), "", "", "echo a\nif then\n", &out, &errOut); err == nil || out.Len() != 0 {
		t.Errorf("err %v, stdout %q: want a parse error and nothing run", err, out.String())
	}
}

// The interactive shell has i in $- and the interactive option set, as
// bash's and zsh's: a startup file's guard for a shell that is not
// interactive lets the rest run. A script or -c line has neither.
func TestExecutor_InteractiveShellFlags(t *testing.T) {
	t.Parallel()
	const probe = `case $- in *i*) echo dash;; esac; [[ -o interactive ]] && echo opt; [[ ! -o interactive ]] || echo notnot; echo "${-}" | grep -q i && echo braces; eval 'case "$-" in *i*) echo eval;; esac'; f() { case $- in *i*) echo func;; esac; }; f`
	for _, tt := range []struct {
		mode terminalMode
		want string
	}{
		{terminalInteractive, "dash\nopt\nnotnot\nbraces\neval\nfunc\n"},
		{terminalPlainInteractive, "dash\nopt\nnotnot\nbraces\neval\nfunc\n"},
		{terminalPlain, ""},
	} {
		e := New()
		e.terminal = tt.mode
		var out, errOut bytes.Buffer
		if _, err := e.Execute(context.Background(), probe, &out, &errOut); err != nil {
			t.Fatal(err)
		}
		if out.String() != tt.want {
			t.Errorf("mode %v: %q (stderr %q), want %q", tt.mode, out.String(), errOut.String(), tt.want)
		}
	}
	// hash -i's shell is interactive, and drives no more of the terminal.
	e := New()
	e.UseInteractiveShell()
	if e.terminal != terminalPlainInteractive {
		t.Errorf("hash -i's mode %v, want terminalPlainInteractive", e.terminal)
	}
	e.terminal = terminalInteractive
	e.UseInteractiveShell()
	if e.terminal != terminalInteractive {
		t.Errorf("the REPL's mode after UseInteractiveShell %v, want terminalInteractive", e.terminal)
	}

	// The Debian .bashrc preamble and zsh's guard, in a startup file.
	e = New()
	e.terminal = terminalInteractive
	var out, errOut bytes.Buffer
	text := "case $- in\n    *i*) ;;\n      *) return;;\nesac\n[[ -o interactive ]] || return\necho rest\n"
	if _, err := e.ExecuteStartup(context.Background(), "", "", text, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "rest\n" || errOut.String() != "" {
		t.Errorf("preamble: stdout %q stderr %q, want rest", out.String(), errOut.String())
	}
	// $- stays one i, through a job's text printed and parsed again.
	out.Reset()
	if _, err := e.Execute(context.Background(), `{ echo "$-"; } & wait`, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Count(got, "i") != 1 {
		t.Errorf("$- in a job: %q, want one i", got)
	}
	// The jobs listing shows the job's $- as it was typed.
	out.Reset()
	if _, err := e.Execute(context.Background(), `{ sleep 0.2; echo "$-"; } & jobs; wait`, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `echo "$-"`) || strings.Contains(got, flagsVar) {
		t.Errorf("jobs: %q, want the job's text as typed", got)
	}
}

// $- has its i for every expansion of it, its operators' too, not only a
// bare $-: nvm's guard `[ "${-#*e}" != "$-" ]` (is errexit on?) read every
// flag as set where only a bare $- had the i, and nvm() called itself
// after set +e forever, hanging the shell's startup.
func TestExecutor_InteractiveFlagsInEveryExpansion(t *testing.T) {
	const probe = `n=0; f() { n=$((n+1)); [ $n -gt 3 ] && { echo loop; return; }; if [ "${-#*e}" != "$-" ]; then set +e; f; return; fi; echo ok; }; f
echo "len=${#-} drop=[${-//i}] dash=$-"
[[ $- == *i* ]] && echo dbl
x=$-; case $x in *i*) echo assigned;; esac
echo "$-" | grep -q i && echo piped
set -e; [ "${-#*e}" != "$-" ] && echo errexit; set +e
eval "$(f2() { case $- in *i*) echo copied;; esac; }; declare -f f2)"; f2`
	e := New()
	e.terminal = terminalInteractive
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := e.Execute(ctx, probe, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	const want = "ok\nlen=1 drop=[] dash=i\ndbl\nassigned\npiped\nerrexit\ncopied\n"
	if out.String() != want {
		t.Errorf("stdout %q (stderr %q), want %q", out.String(), errOut.String(), want)
	}
}

// flagsVar has a key for every value of the interpreter's $-: its set
// options are the ones of posixFlags (and pipefail, with no flag). An
// option a mvdan.cc/sh update adds needs its flag in posixFlags, or every
// $- with it would expand to nothing in the interactive shell.
func TestExecutor_FlagsVarKnowsTheInterpretersFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	if _, err := New().Execute(context.Background(), "set +o", &out, &errOut); err != nil {
		t.Fatal(err)
	}
	want := "set +o allexport\nset +o errexit\nset +o noexec\nset +o noglob\nset +o nounset\nset +o xtrace\nset +o pipefail\n"
	if out.String() != want {
		t.Errorf("the interpreter's options:\n%s\nwant (posixFlags):\n%s", out.String(), want)
	}
	m := interactiveFlags().Map
	if len(m) != 1<<len(posixFlags) || m[""] != "i" || m["aefnux"] != "aefinux" || m["ex"] != "eix" {
		t.Errorf("flagsVar = %v", m)
	}
}

// A sourced file's text goes through the source filter before it is
// parsed; a file it loads none of runs none of it and says so once.
func TestExecutor_SourceFilter(t *testing.T) {
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.zsh"), filepath.Join(dir, "bad.zsh")
	for path, text := range map[string]string{good: "echo zsh-only\necho kept\n", bad: "echo ran\n"} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := New()
	var seen []string
	e.SetSourceFilter(func(path string, src []byte, _, _ string) SourceFile {
		seen = append(seen, path)
		if path == bad {
			return SourceFile{Load: LoadedNone, Msg: "the filter failed"}
		}
		return SourceFile{Text: bytes.ReplaceAll(src, []byte("echo zsh-only\n"), []byte(": skipped\n"))}
	})
	var out, errOut bytes.Buffer
	if _, err := e.Execute(context.Background(), "source "+good+"; . "+bad+"; echo \"rc=$?\"", &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "kept\nrc=2\n" || errOut.String() != "hash: "+bad+": the filter failed\n" {
		t.Errorf("stdout %q stderr %q, want kept, rc=2 and one line for bad.zsh", out.String(), errOut.String())
	}
	if len(seen) != 2 {
		t.Errorf("filtered %q, want both files", seen)
	}
}

// zsh's setup builtins succeed and do nothing in any position (after ;, in a
// function, a pipeline, a sourced file), unless a function of the name is
// defined; shopt -s and -u of bash's interactive options do nothing, quietly,
// and other unsupported ones still fail. bash's bind (fzf's and zoxide's
// key bindings) does nothing and succeeds, quietly: hash has its own.
func TestExecutor_SetupBuiltinsAreNoops(t *testing.T) {
	dir := t.TempDir()
	sourced := filepath.Join(dir, "setup.zsh")
	if err := os.WriteFile(sourced, []byte("zstyle ':x' y z\nautoload -Uz compinit && compinit\necho sourced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, line, out, errOut string }{
		{"after ;", "setopt autocd; echo a", "a\n", ""},
		{"in a function", "f() { unsetopt beep; bindkey -e; zmodload zsh/x; echo \"f=$?\"; }; f", "f=0\n", ""},
		{"in a chain", "compdef _x x && zle -N y && add-zsh-hook precmd p && echo ok", "ok\n", ""},
		{"sourced", "source " + sourced, "sourced\n", ""},
		{"a function wins", "setopt() { echo mine \"$@\"; }; setopt autocd", "mine autocd\n", ""},
		{"shopt", "shopt -s histappend checkwinsize; echo \"rc=$?\"; shopt -u cmdhist; echo \"rc=$?\"", "rc=0\nrc=0\n", ""},
		{"shopt keeps the rest", "shopt -s globstar histappend; shopt -q globstar 2>/dev/null; shopt globstar", "globstar\ton\n", ""},
		{"shopt unknown", "shopt -s no_such_opt; echo \"rc=$?\"", "rc=1\n", "shopt: invalid option name \"no_such_opt\"\n"},
		{"bind", `bind -m emacs-standard '"\er": redraw-current-line'; echo "rc=$?"; bind -x '"\C-t": w'; builtin bind -p; command bind; echo "rc=$?"`, "rc=0\nrc=0\n", ""},
	} {
		e := New()
		var out, errOut bytes.Buffer
		if _, err := e.Execute(context.Background(), tt.line, &out, &errOut); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if out.String() != tt.out || errOut.String() != tt.errOut {
			t.Errorf("%s: stdout %q stderr %q, want %q and %q", tt.name, out.String(), errOut.String(), tt.out, tt.errOut)
		}
	}
}

// is-at-least compares versions as zsh's does; with no version and no
// $ZSH_VERSION it succeeds.
func TestExecutor_IsAtLeast(t *testing.T) {
	const line = `is-at-least 5.1 5.9 && echo a; is-at-least 5.10 5.9 || echo b; is-at-least 5.9 5.9 && echo c; ` +
		`is-at-least 5.9.1 5.9 || echo d; is-at-least 5.9 5.9-test && echo e; is-at-least 5.9 5.10 && echo f; ` +
		`is-at-least 4.3.17-dev 4.3.17 && echo g; is-at-least 99 && echo h; ZSH_VERSION=5.0.8 is-at-least 5.1 || echo i`
	var out, errOut bytes.Buffer
	if _, err := New().Execute(context.Background(), line, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if want := "a\nb\nc\nd\ne\nf\ng\nh\ni\n"; out.String() != want || errOut.Len() != 0 {
		t.Errorf("stdout %q stderr %q, want %q", out.String(), errOut.String(), want)
	}
}

// A sourced file's syntax error that is a zsh feature names the file once,
// with its line, as a parse error does.
func TestExecutor_SourcedZshSyntaxErrorNamesTheFileOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc.sh")
	if err := os.WriteFile(path, []byte("echo a\nx=${(%):-%n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if _, err := New().Execute(context.Background(), "source "+path, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	want := "hash: " + path + ": line 2: parameter expansion flags are a zsh feature; tried parsing as bash\n"
	if errOut.String() != want {
		t.Errorf("stderr %q, want %q", errOut.String(), want)
	}
}

// shopt -q and -oq answer whether the options are on, quietly, as bash's:
// the interpreter refused -q, so the Debian .bashrc's `if ! shopt -oq
// posix` printed an error at each start.
func TestExecutor_ShoptQuery(t *testing.T) {
	for _, tt := range []struct{ name, line, out string }{
		{"posix is off", "shopt -oq posix; echo $?", "1\n"},
		{"the Debian test", "if ! shopt -oq posix; then echo completion; fi", "completion\n"},
		{"a set option", "set -o pipefail; shopt -oq pipefail; echo $?; shopt -qo pipefail; echo $?", "0\n0\n"},
		{"an unset option", "shopt -oq nounset; echo $?", "1\n"},
		{"a shell option", "shopt -q extglob; echo $?; shopt -s extglob; shopt -q extglob; echo $?", "1\n0\n"},
		{"all must be on", "shopt -s extglob; shopt -q extglob dotglob; echo $?", "1\n"},
		{"set quietly", "shopt -sq dotglob; shopt -q dotglob; echo $?", "0\n"},
		{"an interactive option", "shopt -s histappend; shopt -q histappend; echo $?", "1\n"},
		{"an unknown option", "shopt -q no_such_opt; echo $?", "1\n"},
		{"no option", "shopt -q; echo $?", "0\n"},
		{"a quoted name", "shopt -q 'a b'\\''c'; echo $?", "1\n"},
	} {
		var out, errOut bytes.Buffer
		if _, err := New().Execute(context.Background(), tt.line, &out, &errOut); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if out.String() != tt.out || errOut.Len() != 0 {
			t.Errorf("%s: stdout %q stderr %q, want %q and nothing", tt.name, out.String(), errOut.String(), tt.out)
		}
	}
}

// A file source reads that does not parse runs its statements before the
// syntax error, as bash's source does, and sets status 2, and the error is
// printed. In a startup file none of it runs and it is the shell's to say
// (StartupResult.NotLoaded), with its line: the statements before the
// error ran there too, and oh-my-zsh.sh's ran its guard, which printed an
// error and the process tree at each start.
func TestExecutor_ASourcedFileThatDoesNotParse(t *testing.T) {
	dir := t.TempDir()
	broken, none := filepath.Join(dir, "broken.sh"), filepath.Join(dir, "none.sh")
	for path, text := range map[string]string{
		broken: "f() { echo f; }\necho before\nif then\necho after\n",
		none:   "if then\necho after\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line := "source " + broken + "; echo \"rc=$?\"; f; . " + none + "; echo \"rc=$?\""
	const want = "before\nrc=2\nf\nrc=2\n"

	var out, errOut bytes.Buffer
	if _, err := New().Execute(context.Background(), line, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(errOut.String(), "hash: "+broken+": line 3: ") || strings.Count(errOut.String(), "\n") != 2 {
		t.Errorf("stderr %q, want a line for each file", errOut.String())
	}
	if out.String() != want {
		t.Errorf("stdout %q, want %q", out.String(), want)
	}

	out.Reset()
	errOut.Reset()
	line = "source " + broken + "; echo \"rc=$?\"; type f >/dev/null 2>&1 || echo no-f; . " + none + "; echo \"rc=$?\""
	res, err := New().ExecuteStartup(context.Background(), "", "", line, &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if want := "rc=2\nno-f\nrc=2\n"; out.String() != want || errOut.Len() != 0 {
		t.Errorf("startup: stdout %q stderr %q, want %q and nothing", out.String(), errOut.String(), want)
	}
	if len(res.NotLoaded) != 2 || res.NotLoaded[0].Path != broken || res.NotLoaded[0].Line != 3 || res.NotLoaded[0].Load != LoadedNone ||
		res.NotLoaded[1].Path != none || res.NotLoaded[1].Line != 1 || res.NotLoaded[1].Load != LoadedNone || res.NotLoaded[0].Msg == "" {
		t.Errorf("startup: not loaded %+v, want broken.sh from line 3 and none.sh", res.NotLoaded)
	}
}

// A startup file that runs past the threshold shows the terminal its name
// as the program status's message: its whole text, flattened, was the
// message (Rex showed a .bashrc's every line as the running command).
func TestExecuteStartup_ReportsTheFilesName(t *testing.T) {
	t.Parallel()
	e, buf := newStatusExecutor(t, 10*time.Millisecond)
	if _, err := e.ExecuteStartup(context.Background(), "~/.hashrc", "", "export A=1\nsleep 0.2\n", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := cmdReport(programstatus.Working, "sleep", "~/.hashrc") + cmdReport(programstatus.Done, "sleep", "~/.hashrc")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

// In a startup file, the statements the source filter keeps of a file
// that does not load all (the lines before one it stops at) run, as they
// run when the file is a startup step: only a file that does not parse
// loads none of it there. A migrated .bashrc a migrated .bash_profile
// sources said "not loaded: line 3" at startup and "not loaded from line
// 3" in hash migrate status.
func TestExecuteStartup_TheFiltersStatementsRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc.sh")
	if err := os.WriteFile(path, []byte("echo before\nif x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New()
	e.SetSourceFilter(func(_ string, _ []byte, _, _ string) SourceFile {
		return SourceFile{Load: LoadedBefore, Text: []byte("echo before\n"), Line: 2, Msg: "stop"}
	})
	var out, errOut bytes.Buffer
	res, err := e.ExecuteStartup(context.Background(), "", "", "source "+path+"; echo \"rc=$?\"", &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "before\nrc=2\n" || errOut.Len() != 0 {
		t.Errorf("stdout %q stderr %q, want before and rc=2", out.String(), errOut.String())
	}
	if len(res.NotLoaded) != 1 || res.NotLoaded[0].Load != LoadedBefore || res.NotLoaded[0].Line != 2 {
		t.Errorf("not loaded %+v, want the file from line 2, the lines before it run", res.NotLoaded)
	}
}
