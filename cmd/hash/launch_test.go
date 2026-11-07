package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/compat"
)

var (
	builtHash    string
	builtHashErr error
	buildOnce    sync.Once
)

// hashBinary builds hash once for the package's launch tests, with the race
// detector under go test -race.
func hashBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "hash-launch-test")
		if err != nil {
			builtHashErr = err
			return
		}
		builtHash = filepath.Join(dir, "hash")
		args := []string{"build", "-o", builtHash}
		if raceBuild {
			args = append(args, "-race")
		}
		if out, err := exec.Command("go", append(args, ".")...).CombinedOutput(); err != nil {
			builtHashErr = errors.New(err.Error() + "\n" + string(out))
		}
	})
	if builtHashErr != nil {
		t.Fatalf("build hash: %v", builtHashErr)
	}
	return builtHash
}

func TestMain(m *testing.M) {
	if status := os.Getenv(leaderStatusEnv); status != "" {
		leadSession(status, os.Args[1:])
	}
	code := m.Run()
	if builtHash != "" {
		_ = os.RemoveAll(filepath.Dir(builtHash))
	}
	os.Exit(code)
}

// cappedBuffer keeps at most limit bytes: a hash that loops writing must
// fail the test, not fill the disk or memory.
type cappedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
	over  bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.buf.Len()+len(p) > c.limit {
		c.over = true
		return 0, errors.New("output over the test's cap")
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

type launch struct {
	stdout, stderr string
	code           int
}

// runHash runs the hash binary with stdin (not a terminal) and args, in an
// isolated home, under a hard kill timeout.
func runHash(t *testing.T, dir, stdin string, args ...string) launch {
	t.Helper()
	return runHashHome(t, t.TempDir(), dir, stdin, args...)
}

// runHashHome is runHash with the given home directory.
func runHashHome(t *testing.T, home, dir, stdin string, args ...string) launch {
	t.Helper()
	bin := hashBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	cmd.Env = isolatedEnv(home)
	cmd.Stdin = strings.NewReader(stdin)
	stdout := &cappedBuffer{limit: 1 << 20}
	stderr := &cappedBuffer{limit: 1 << 20}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("hash %q did not finish in time\nstdout: %.500q\nstderr: %.500q", args, stdout.String(), stderr.String())
	}
	if stdout.over || stderr.over {
		t.Fatalf("hash %q wrote past the output cap\nstdout: %.500q", args, stdout.String())
	}
	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("run hash: %v", err)
	}
	return launch{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// isolatedEnv is the test's environment without the parent's hash markers,
// with home and the XDG roots in home.
func isolatedEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HASH_") || strings.HasPrefix(kv, "XDG_") || strings.HasPrefix(kv, "HOME=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"HASH_CONFIG_DIR="+filepath.Join(home, ".config", "hash"),
	)
}

func (l launch) want(t *testing.T, stdout string, code int) {
	t.Helper()
	if l.stdout != stdout || l.code != code {
		t.Errorf("stdout = %q, status %d; want %q, status %d\nstderr: %q", l.stdout, l.code, stdout, code, l.stderr)
	}
}

// Short options combine as in bash, so tools that run `$SHELL -lc cmd` get
// the command run.
func TestLaunch_CombinedShortOptions(t *testing.T) {
	runHash(t, "", "", "-lc", "echo $HASH_LOGIN").want(t, "1\n", 0)
	runHash(t, "", "", "-i", "-l", "-c", "echo ok").want(t, "ok\n", 0)
}

// Options end at the command string: a word after it is a positional
// parameter, never an option of hash's.
func TestLaunch_OptionsEndAtTheCommand(t *testing.T) {
	runHash(t, "", "", "-c", `echo "$0 $1 ${HASH_LOGIN-unset}"`, "-l", "-v").want(t, "-l -v unset\n", 0)
}

func TestLaunch_InvalidOption(t *testing.T) {
	l := runHash(t, "", "", "--bogus")
	if l.code != 2 || !strings.Contains(l.stderr, "--bogus: invalid option") || l.stdout != "" {
		t.Errorf("hash --bogus: status %d, stdout %q, stderr %q; want status 2 and an invalid option message", l.code, l.stdout, l.stderr)
	}
}

func noEscapes(t *testing.T, l launch) {
	t.Helper()
	if strings.Contains(l.stdout, "\x1b") || strings.Contains(l.stderr, "\x1b") {
		t.Errorf("escape sequences in a non-interactive shell's output\nstdout: %q\nstderr: %q", l.stdout, l.stderr)
	}
}

// A stdin that is not a terminal is a script, as for bash: exactly the
// commands' output, no prompt or editor, and the status exit set.
func TestLaunch_PipedStdinIsAScript(t *testing.T) {
	l := runHash(t, "", "echo hi\nexit 3\necho no\n")
	l.want(t, "hi\n", 3)
	noEscapes(t, l)
	if l.stderr != "" {
		t.Errorf("stderr = %q, want nothing", l.stderr)
	}
}

func TestLaunch_PipedScriptStatusIsTheLastCommands(t *testing.T) {
	runHash(t, "", "echo a\nfalse\n").want(t, "a\n", 1)
	runHash(t, "", "false\ntrue").want(t, "", 0)
	runHash(t, "", "").want(t, "", 0)
	runHash(t, "", "trap 'echo bye' EXIT\necho hi\n").want(t, "hi\nbye\n", 0)
}

// Commands spanning lines run once they are complete.
func TestLaunch_PipedScriptMultilineCommands(t *testing.T) {
	src := "if true; then\n  echo yes\nfi\n" +
		"f() {\n  echo \"f $1\"\n}\nf one\n" +
		"cat <<EOF\nbody $((1+1))\nEOF\n" +
		"echo a \\\n  b\n" +
		"echo 'two\nlines'\n" +
		"true &&\n  echo and\n"
	runHash(t, "", src).want(t, "yes\nf one\nbody 2\na b\ntwo\nlines\nand\n", 0)
}

// The script's commands read the rest of stdin, as in bash: hash reads a
// line at a time and leaves the rest for them. (A command that reads ahead,
// like head, takes more than its line, in bash too.)
func TestLaunch_PipedScriptCommandsReadTheRestOfStdin(t *testing.T) {
	runHash(t, "", "read x\nfrom-read\necho \"got $x\"\nread y; echo \"and $y\"\nnext\necho after\n").
		want(t, "got from-read\nand next\nafter\n", 0)
	runHash(t, "", "cat\nfrom-cat\n").want(t, "from-cat\n", 0)
}

// Startup files follow bash: a login shell (-l, or argv0 starting with -)
// reads the login files even when it is not interactive (`$SHELL -l -c env`
// is how editors find the user's PATH), -i reads the interactive rc files,
// and a plain -c, a script or a piped stdin reads none. Nothing offers the
// migration.
func TestLaunch_StartupFilesFollowBash(t *testing.T) {
	home := t.TempDir()
	for name, body := range map[string]string{
		".hashrc":       "echo from-hashrc\n",
		".hash_profile": "echo from-profile\n",
		".profile":      "echo from-dot-profile\n",
		".zshrc":        "alias ll='ls -l'\n",
	} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const login = "from-dot-profile\nfrom-profile\n"
	for _, tt := range []struct {
		stdin string
		args  []string
		want  string
	}{
		{"echo ran\n", nil, "ran\n"},
		{"", []string{"-c", "echo ran"}, "ran\n"},
		{"echo ran\n", []string{"-l"}, login + "ran\n"},
		{"", []string{"-lc", "echo ran"}, login + "ran\n"},
		{"", []string{"-ic", "echo ran"}, "from-hashrc\nran\n"},
		{"", []string{"-ilc", "echo ran"}, login + "from-hashrc\nran\n"},
		{"", []string{"-l", "--noprofile", "-c", "echo ran"}, login + "ran\n"}, // accepted, no effect
	} {
		l := runHashHome(t, home, "", tt.stdin, tt.args...)
		l.want(t, tt.want, 0)
		noEscapes(t, l)
	}
}

// hash -i is an interactive shell, as bash -i: $- has an i, in its rc
// files and its command, so the Debian .bashrc guard lets the rest run. A
// plain -c has none.
func TestLaunch_DashIHasTheInteractiveFlag(t *testing.T) {
	home := t.TempDir()
	writeScript(t, home, ".hashrc", "case $- in\n    *i*) ;;\n      *) return;;\nesac\necho rc-ran\n")
	const probe = `case $- in *i*) echo i;; *) echo none;; esac; [[ -o interactive ]] && echo opt`
	runHashHome(t, home, "", "", "-ic", probe).want(t, "rc-ran\ni\nopt\n", 0)
	runHashHome(t, home, "", "", "-ilc", probe).want(t, "rc-ran\ni\nopt\n", 0)
	runHashHome(t, home, "", "", "-c", probe).want(t, "none\n", 1)
}

// bash's set options on the command line apply to the program.
func TestLaunch_SetOptionsApplyToTheProgram(t *testing.T) {
	runHash(t, "", "", "-e", "-c", "false; echo no").want(t, "", 1)
	runHash(t, "", "", "-o", "pipefail", "-c", "false | true").want(t, "", 1)
	runHash(t, "", "", "-eo", "pipefail", "-c", "false | true; echo no").want(t, "", 1)
	runHash(t, "", "", "-e", "+e", "-c", "false; echo yes").want(t, "yes\n", 0)
	if l := runHash(t, "", "", "-x", "-c", "echo hi"); l.stdout != "hi\n" || !strings.Contains(l.stderr, "+ echo hi") {
		t.Errorf("hash -x -c: stdout %q, stderr %q; want hi and a trace", l.stdout, l.stderr)
	}
	if l := runHash(t, "", "", "-u", "-c", "echo $nope; echo no"); l.stdout != "" || l.code == 0 {
		t.Errorf("hash -u -c: stdout %q, status %d; want an unbound variable error", l.stdout, l.code)
	}
	if l := runHash(t, "", "", "-o", "bogus", "-c", "echo no"); l.stdout != "" || l.code != 2 {
		t.Errorf("hash -o bogus: stdout %q, status %d, stderr %q; want status 2", l.stdout, l.code, l.stderr)
	}
	runHash(t, "", "echo $1\n", "-", "-s", "x").want(t, "", 127) // - ends the options: -s is a script name
}

func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// hash script.sh a b runs the file with $0 the script and $1, $2 the
// arguments, as bash does; options after the script are its arguments.
func TestLaunch_ScriptFileWithArguments(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "script.sh", "#!/usr/bin/env hash\necho \"$0 $1 $2 $#\"\nexit 4\n")
	runHash(t, dir, "", "script.sh", "a", "b").want(t, "script.sh a b 2\n", 4)
	runHash(t, dir, "", "script.sh", "-c", "-l").want(t, "script.sh -c -l 2\n", 4)
	runHash(t, dir, "", "--", "script.sh", "x").want(t, "script.sh x  1\n", 4)
}

// The script's stdin is hash's stdin, not the script.
func TestLaunch_ScriptFileReadsStdin(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "script.sh", "read x\necho \"got $x\"\n")
	runHash(t, dir, "from-stdin\n", "script.sh").want(t, "got from-stdin\n", 0)
}

// A script that cannot be read fails the way bash's does: 127 when it is
// missing, 126 when it cannot be read.
func TestLaunch_ScriptFileUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, msg string
		code      int
	}{
		{"nope.sh", "hash: nope.sh: no such file or directory", 127},
		{"adir", "hash: adir: is a directory", 126},
	} {
		l := runHash(t, dir, "", tt.name)
		if l.code != tt.code || strings.TrimSpace(l.stderr) != tt.msg || l.stdout != "" {
			t.Errorf("hash %s: status %d, stderr %q, stdout %q; want %d and %q", tt.name, l.code, l.stderr, l.stdout, tt.code, tt.msg)
		}
	}
}

// -s reads the script from stdin and makes the words after the options $1,
// $2, ....
func TestLaunch_DashSReadsStdinWithArguments(t *testing.T) {
	runHash(t, "", "echo \"$0 $1 $2\"\n", "-s", "a", "b").want(t, "hash a b\n", 0)
}

// An agent request needs the interactive shell: in -c or a script, a command
// named ?? is refused with status 127, never globbed into a two-letter file
// and run.
func TestLaunch_AgentRequestRefusedWithoutATerminal(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "ab", "") // ?? would glob to it
	const msg = "??: the agent needs an interactive shell"

	l := runHash(t, dir, "", "-c", "?? delete everything")
	if l.code != 127 || l.stdout != "" || strings.TrimSpace(l.stderr) != "hash: line 1: "+msg {
		t.Errorf("hash -c '?? ...': status %d, stdout %q, stderr %q; want 127 and %q", l.code, l.stdout, l.stderr, msg)
	}

	l = runHash(t, dir, "echo before\nls | ?? summarize\necho $?\n?? why\n")
	l.want(t, "before\n127\n", 127)
	if want := "hash: line 2: " + msg + "\nhash: line 4: " + msg + "\n"; l.stderr != want {
		t.Errorf("stderr = %q, want %q", l.stderr, want)
	}

	// Quoted, it is a word; as an argument, a glob, as in bash.
	runHash(t, dir, "", "-c", `echo "??" ??`).want(t, "?? ab\n", 0)
}

// A syntax error ends a non-interactive shell with status 2, as in bash,
// after the commands before it ran; the message names the script's line.
func TestLaunch_SyntaxErrorEndsTheScriptWithStatus2(t *testing.T) {
	l := runHash(t, "", "", "-c", "if")
	if l.code != 2 || l.stderr != "hash: line 1: `if` must be followed by a statement list\n" {
		t.Errorf("hash -c if: status %d, stderr %q; want 2 and the error on line 1", l.code, l.stderr)
	}

	l = runHash(t, "", "echo a\n\necho )\necho b\n")
	l.want(t, "a\n", 2)
	if want := "hash: line 3: a command can only contain words and redirects; encountered `)`\n"; l.stderr != want {
		t.Errorf("stderr = %q, want %q", l.stderr, want)
	}

	l = runHash(t, "", "echo a\nif true; then\n  echo b\n")
	l.want(t, "a\n", 2)
	if !strings.HasPrefix(l.stderr, "hash: line 2: ") {
		t.Errorf("stderr = %q, want the error on line 2", l.stderr)
	}
}

// An expansion error (${x:?msg}, set -u and an unset variable) ends a
// script with status 127, as in bash 5.2; in a subshell it ends only the
// subshell, with 1.
func TestLaunch_ExpansionErrorEndsTheScript(t *testing.T) {
	runHash(t, "", "", "-c", "echo ${x:?msg}; echo after").want(t, "", 127)
	runHash(t, "", "set -u\necho $nope\necho after\n").want(t, "", 127)
	runHash(t, "", "", "-c", `y=$(echo ${x:?msg}); echo "after=$?"`).want(t, "after=1\n", 0)
}

// A non-interactive shell has no agent or copy to keep output for: a large
// output passes through without the interactive shell's truncation note.
func TestLaunch_LargeOutputHasNoTruncationNote(t *testing.T) {
	bin := hashBinary(t)
	for _, tt := range []struct {
		stdin string
		args  []string
	}{
		{"", []string{"-c", "yes | head -c 3000000"}},
		{"yes | head -c 3000000\n", nil},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, bin, tt.args...)
		cmd.Env = isolatedEnv(t.TempDir())
		cmd.Stdin = strings.NewReader(tt.stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run() // stdout is /dev/null
		cancel()
		if err != nil || stderr.String() != "" {
			t.Errorf("hash %q: err %v, stderr %q; want no error and no stderr", tt.args, err, stderr.String())
		}
	}
}

// A backslash ends a line's command only where the shell reads it as an
// escape: not in a comment or single quotes. A script line that hash took
// for a continued command was read as code, not left for the command
// before it to read.
func TestLaunch_PipedScriptBackslashInCommentEndsTheLine(t *testing.T) {
	runHash(t, "", "read x # note \\\nDATA\necho \"got $x\"\n").want(t, "got DATA\n", 0)
	runHash(t, "", "echo 'a\\\nb'\necho c\n").want(t, "a\\\nb\nc\n", 0)
	runHash(t, "", "echo a \\\n  b\n").want(t, "a b\n", 0)
}

// A script on stdin from a file leaves the rest of the file to its
// commands, as from a pipe.
func TestLaunch_FileStdinCommandsReadTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sh")
	if err := os.WriteFile(path, []byte("read x\nfrom-read\necho \"got $x\"\ncat\nfrom-cat\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, hashBinary(t))
	cmd.Env = isolatedEnv(t.TempDir())
	cmd.Stdin = f
	out, err := cmd.Output()
	if err != nil || string(out) != "got from-read\nfrom-cat\n" {
		t.Fatalf("hash < file: %q, %v; want got from-read and from-cat", out, err)
	}
}

// A non-interactive hash does not wait for its background jobs, as bash
// does not: a script that starts a server or `sleep 1000 &` and exits must
// not hang (kill $! cannot stop a job, see README). A job hash runs itself
// (here a subshell) is cut off when hash exits, where bash's goes on as a
// process of its own; the EXIT trap still runs. (The sleep writes nowhere:
// it would hold the test's pipe open, with bash too.)
func TestLaunch_ExitDoesNotWaitForBackgroundJobs(t *testing.T) {
	t.Parallel()
	runHash(t, "", "", "-c", `(sleep 2 >/dev/null 2>&1; echo late) & echo now`).want(t, "now\n", 0)
	runHash(t, "", "(sleep 2 >/dev/null 2>&1; echo late) &\nexit 3\n").want(t, "", 3)
	runHash(t, "", "", "-c", `trap "echo trap" EXIT; (sleep 2 >/dev/null 2>&1; echo late) & echo now`).want(t, "now\ntrap\n", 0)
}

// A background command keeps running after hash exits, and hash returns at
// once. (Its stderr goes to /dev/null too: a command still holding the
// test's pipe would make the test wait for it, as it would with bash.)
func TestLaunch_BackgroundCommandDoesNotHoldTheScript(t *testing.T) {
	const sleeper = "sleep 30.4321" // unique, to find and stop it
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", sleeper).Run() })
	start := time.Now()
	runHash(t, "", "", "-c", sleeper+` >/dev/null 2>&1 &`).want(t, "", 0)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("hash -c '%s &' took %v, want it to return at once", sleeper, took)
	}
	if err := exec.Command("pgrep", "-f", sleeper).Run(); err != nil {
		t.Errorf("the background sleep is gone after hash exited (pgrep: %v), want it running", err)
	}
}

func TestLaunch_StartupFileTildeUser(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{"hash_no_such_user", ".config/hash"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeScript(t, filepath.Join(home, "hash_no_such_user"), "rc", "echo wrong-file\n")
	writeScript(t, home, ".rc", "echo from-rc\n")
	writeScript(t, filepath.Join(home, ".config", "hash"), "config.toml", "[shell.startup_files]\ninteractive = [\"~hash_no_such_user/rc\", \"~/.rc\"]\n")
	runHashHome(t, home, "", "", "-ic", "echo ran").want(t, "from-rc\nran\n", 0)
}

// read -p prompts only when its input is a terminal, as in bash: a script
// reading a pipe or a file printed the prompt into its output.
func TestLaunch_ReadPromptsOnlyAtATerminal(t *testing.T) {
	l := runHash(t, "", "bob\n", "-c", `read -p "Name: " n; echo "[$n]"`)
	l.want(t, "[bob]\n", 0)
	if l.stderr != "" {
		t.Errorf("stderr = %q, want no prompt", l.stderr)
	}
	runHash(t, "", "read -rp 'Name: ' n\nbob\necho \"[$n]\"\n").want(t, "[bob]\n", 0)
}

// hash -ic and -lc run their startup files as the interactive shell does:
// an expansion error ends its file and the -i shell goes on (a
// non-interactive one exits, as bash), exit ends the shell, and a zsh file
// an rc file sources is filtered.
func TestLaunch_StartupFilesRunAsTheInteractiveShellRunsThem(t *testing.T) {
	home := t.TempDir()
	writeScript(t, home, ".hash_profile", "echo profile\n: ${HASH_T_UNSET:?unset}\necho no\n")
	writeScript(t, home, "plug.zsh", "local -a arr=(${(f)\"$(echo a)\"})\nexport PLUG=1\n")
	writeScript(t, home, ".hashrc", "source ~/plug.zsh\necho \"rc PLUG=$PLUG\"\n: ${HASH_T_UNSET:?unset}\necho no\n")
	runHashHome(t, home, "", "", "-ic", "echo ran").want(t, "rc PLUG=1\nran\n", 0)
	runHashHome(t, home, "", "", "-lc", "echo ran").want(t, "profile\n", 127)

	writeScript(t, home, ".hashrc", "echo rc\nexit 5\necho no\n")
	runHashHome(t, home, "", "", "-ic", "echo ran").want(t, "rc\n", 5)
}

// hash -lc reads the migrated files the previous shell reads as a login
// shell, -ic those it reads as an interactive one, both every shell's
// (.zshenv): `$SHELL -l -c env` finds the PATH the migrated .zprofile sets.
// A plain -c reads none.
func TestLaunch_ScriptReadsTheMigratedFilesOfItsMode(t *testing.T) {
	home := t.TempDir()
	writeScript(t, home, ".zshenv", "export E=1\n")
	writeScript(t, home, ".zprofile", "export P=1\n")
	writeScript(t, home, ".zshrc", "export R=1\n")
	var paths []string
	for _, name := range []string{".zshenv", ".zprofile", ".zshrc"} {
		paths = append(paths, filepath.Join(home, name))
	}
	state := &compat.State{Status: compat.StatusImported, SourceShell: "zsh", Paths: paths, Import: &compat.ImportRecord{}}
	if err := state.Save(filepath.Join(home, ".local", "share", "hash", "migration.json")); err != nil {
		t.Fatal(err)
	}
	const probe = `echo "e=$E p=$P r=$R"`
	runHashHome(t, home, "", "", "-c", probe).want(t, "e= p= r=\n", 0)
	runHashHome(t, home, "", "", "-lc", probe).want(t, "e=1 p=1 r=\n", 0)
	runHashHome(t, home, "", "", "-ic", probe).want(t, "e=1 p= r=1\n", 0)
	runHashHome(t, home, "", "", "-ilc", probe).want(t, "e=1 p=1 r=1\n", 0)
}
