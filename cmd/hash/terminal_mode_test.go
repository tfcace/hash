package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// runHashOnTTY runs hash with a terminal for stdin (and for stdout when
// stdout is nil, read back from the terminal), as a person's shell would
// run `hash -c ...`, under a hard kill timeout. It returns what reached the
// terminal.
func runHashOnTTY(t *testing.T, stdout *os.File, args ...string) (term, ttyName string) {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer ptmx.Close()
	ttyName = tty.Name()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, hashBinary(t), args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = isolatedEnv(t.TempDir())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if stdout != nil {
		cmd.Stdout = stdout
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tty.Close()
	var out bytes.Buffer
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(&out, ptmx) // ends with EIO once hash and its children closed the tty
		close(copied)
	}()
	_ = cmd.Wait()
	if ctx.Err() != nil {
		t.Fatalf("hash %q did not finish in time", args)
	}
	select {
	case <-copied:
	case <-time.After(2 * time.Second):
	}
	return out.String(), ttyName
}

// A non-interactive hash writes no terminal escapes of its own, even when
// its stdin is a terminal: `hash -c 'echo a | cat' > file` from a person's
// shell used to write OSC 133;C into the file.
func TestLaunch_CommandOnATerminalWritesNoEscapes(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "out.txt")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	term, _ := runHashOnTTY(t, f, "-c", "echo a | cat; /bin/echo b")
	f.Close()
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a\nb\n" {
		t.Errorf("file = %q, want %q", got, "a\nb\n")
	}
	if strings.Contains(term, "\x1b") {
		t.Errorf("terminal got escapes: %q", term)
	}
}

// A non-interactive hash runs its commands on its own terminal, as bash
// does, never on a PTY of its own: `tty` names hash's terminal, and a
// command that changes the terminal's modes (stty) changes them for the next.
func TestLaunch_CommandSeesHashsTerminal(t *testing.T) {
	// Delimit the tty output ourselves: some uutils versions omit its
	// newline. The terminal's identity and modes are what this tests.
	term, ttyName := runHashOnTTY(t, nil, "-c", "tty; printf '\\n'; stty -echo; stty -a")
	lines := strings.Split(strings.ReplaceAll(term, "\r\n", "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != ttyName {
		t.Errorf("tty printed %q, want hash's terminal %s\nterminal: %q", lines[0], ttyName, term)
	}
	if !slices.Contains(strings.Fields(term), "-echo") {
		t.Errorf("stty -echo did not reach the next command's terminal\nterminal: %q", term)
	}
	if strings.Contains(term, "\x1b") {
		t.Errorf("terminal got escapes: %q", term)
	}
}

// An exit in the EXIT trap sets hash's exit status, as in bash, in every
// mode; a trap that does not exit keeps the status the shell had.
func TestLaunch_ExitTrapExitSetsTheStatus(t *testing.T) {
	runHash(t, "", "", "-c", `trap "exit 9" EXIT; exit 3`).want(t, "", 9)
	runHash(t, "", "trap 'exit 9' EXIT\nexit 3\n").want(t, "", 9)
	runHash(t, "", "", "-c", `trap "echo bye; false" EXIT; exit 3`).want(t, "bye\n", 3)
}

// The interactive shell runs the EXIT trap when it exits, and an exit in it
// sets hash's status.
func TestInteractive_ExitTrapExitSetsTheStatus(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(hashBinary(t))
	cmd.Env = isolatedEnv(interactiveHome(t))
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer ptmx.Close()
	var term bytes.Buffer
	go io.Copy(&term, ptmx) //nolint:errcheck // drain the terminal
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	_, _ = ptmx.WriteString("trap 'exit 9' EXIT\r")
	time.Sleep(300 * time.Millisecond)
	_, _ = ptmx.WriteString("exit 3\r")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("hash did not exit")
	}
	if code := cmd.ProcessState.ExitCode(); code != 9 {
		t.Errorf("exit status = %d, want 9 (the EXIT trap's exit)\nterminal: %q", code, term.String())
	}
}

// interactiveHome is a home for an interactive hash that skips the
// first-run welcome, so typed lines reach the prompt.
func interactiveHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "hash")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".welcome_shown"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// No daily registry check against the claude-agent-acp the machine has
	// installed: its "update available" notice lands in the terminal.
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agent]\nauto_update = \"off\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// HASH_INTERACTIVE and HASH_LOGIN say what this hash is: a non-interactive
// hash started from an interactive one must not inherit them.
func TestLaunch_ModeMarkersAreNotInherited(t *testing.T) {
	const probe = `echo "${HASH_INTERACTIVE-unset} ${HASH_LOGIN-unset}"`
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", probe}, "unset unset\n"},
		{[]string{"-ic", probe}, "1 unset\n"},
		{[]string{"-lc", probe}, "unset 1\n"},
	} {
		bin := hashBinary(t)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, bin, tt.args...)
		cmd.Env = append(isolatedEnv(t.TempDir()), "HASH_INTERACTIVE=1", "HASH_LOGIN=1")
		out, err := cmd.Output()
		cancel()
		if err != nil || string(out) != tt.want {
			t.Errorf("hash %q with both markers set: %q, %v; want %q", tt.args, out, err, tt.want)
		}
	}
}

// ttyProbe reports whether the command's stdout and stderr are terminals,
// each on the stream it tests.
const ttyProbe = `sh -c '[ -t 1 ] && echo out-tty || echo out-NOT-tty; [ -t 2 ] && echo err-tty >&2 || echo err-NOT-tty >&2'`

// A non-interactive hash hands its commands its own stdout and stderr, as
// bash does: on a terminal they are the terminal, so `tmux new-window htop`,
// vim's :!cmd and git's editor (all `$SHELL -c`) can draw. Redirected to a
// file or a pipe they are that file or pipe.
func TestLaunch_CommandsGetHashsStdoutAndStderr(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "probe.sh", ttyProbe+"\n")
	for _, args := range [][]string{{"-c", ttyProbe}, {filepath.Join(dir, "probe.sh")}} {
		term, _ := runHashOnTTY(t, nil, args...)
		for _, want := range []string{"out-tty", "err-tty"} {
			if !strings.Contains(term, want+"\r\n") {
				t.Errorf("hash %q: terminal %q, want %s", args, term, want)
			}
		}

		outPath := filepath.Join(dir, "out.txt")
		f, err := os.Create(outPath)
		if err != nil {
			t.Fatal(err)
		}
		term, _ = runHashOnTTY(t, f, args...)
		f.Close()
		got, _ := os.ReadFile(outPath)
		if string(got) != "out-NOT-tty\n" || !strings.Contains(term, "err-tty") {
			t.Errorf("hash %q > file: file %q, terminal %q; want out-NOT-tty in the file, err-tty on the terminal", args, got, term)
		}

		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		piped := make(chan []byte)
		go func() {
			b, _ := io.ReadAll(r)
			piped <- b
		}()
		_, _ = runHashOnTTY(t, w, args...)
		w.Close()
		if got := <-piped; string(got) != "out-NOT-tty\n" {
			t.Errorf("hash %q | cat: pipe got %q, want out-NOT-tty", args, got)
		}
		r.Close()
	}
}

// A background command of a non-interactive hash writes to hash's terminal
// itself, as in bash; it used to get a pipe hash copied from.
func TestLaunch_BackgroundCommandGetsHashsTerminal(t *testing.T) {
	term, _ := runHashOnTTY(t, nil, "-c", ttyProbe+" & wait")
	for _, want := range []string{"out-tty", "err-tty"} {
		if !strings.Contains(term, want+"\r\n") {
			t.Errorf("terminal %q, want %s", term, want)
		}
	}
}
