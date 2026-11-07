package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Output a background job writes after its line ended goes to the terminal,
// not into the finished line's writers (a caller's buffer, or the capture).
func TestExecute_BackgroundOutputAfterLineGoesToTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	origStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	e := New()
	var buf bytes.Buffer
	res, err := e.Execute(context.Background(), `(sleep 0.2; echo late) &`, &buf, io.Discard)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The next line has writers of its own, which the job's output does not
	// reach either.
	var next bytes.Buffer
	if _, err := e.Execute(context.Background(), "true", &next, io.Discard); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	os.Stdout = origStdout
	w.Close()
	term, _ := io.ReadAll(r)

	if strings.Contains(buf.String(), "late") || strings.Contains(res.CapturedOutput, "late") || strings.Contains(next.String(), "late") {
		t.Fatalf("background output landed in a line: buf=%q capture=%q next=%q", buf.String(), res.CapturedOutput, next.String())
	}
	if !strings.Contains(string(term), "late") {
		t.Fatalf("background output %q, want it on the terminal", term)
	}
}

// A background job's refusal belongs to no later line's Result.
func TestExecute_BackgroundRefusalStaysOutOfLaterLines(t *testing.T) {
	t.Parallel()
	e := New()
	e.SetRefusalHandler(func(error) {})
	if _, err := e.Execute(context.Background(), `(sleep 0.1; hash_no_such_cmd_xyz) &`, io.Discard, io.Discard); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The job's refusal arrives while the next line runs.
	res, err := e.Execute(context.Background(), `sleep 0.4`, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Refusals) != 0 {
		t.Fatalf("later line reported %v", res.Refusals)
	}
}

// A background job runs detached: no PTY (which would put the real
// terminal in raw mode and copy keystrokes to the job for its lifetime) and
// no terminal stdin, as in a shell without job control. Its output is the
// terminal itself, as a bash job's.
func TestExecute_BackgroundJobIsDetached(t *testing.T) {
	t.Parallel()
	rec := filepath.Join(t.TempDir(), "rec")
	probe := `sh -c 'in=in-notty; [ -t 0 ] && in=in-tty; exec 3>&1; { printf "%s " $in; tty 0<&3; } > ` + rec + `'`
	for _, line := range []string{
		probe + " & wait",
		// Functions and nested subshells inside the job are detached too.
		"probe() { " + probe + "; }; ( probe ) & wait",
	} {
		_ = os.Remove(rec)
		_, terminal := ttyShellAt(t, New(), line)
		data, _ := os.ReadFile(rec)
		if got := strings.TrimSpace(string(data)); got != "in-notty "+terminal {
			t.Errorf("%q: background probe = %q, want in-notty %s (the line's own terminal)", line, got, terminal)
		}
	}
	// The foreground still gets the terminal, on a PTY of its own.
	if got := ttyShell(t, `sh -c '[ -t 0 ] && [ -t 1 ] && echo tty'`); !strings.Contains(got, "tty") {
		t.Fatalf("foreground probe = %q, want tty", got)
	}
}

// A background job that outlives its line writes to the terminal, never into
// a later line's writers or capture.
func TestExecute_BackgroundOutputStaysOutOfLaterLines(t *testing.T) {
	t.Parallel()
	e := New()
	if _, err := e.Execute(context.Background(), `(sleep 0.1; echo late-bg) &`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res, err := e.Execute(context.Background(), `sleep 0.4; echo fg`, &buf, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != "fg\n" || res.CapturedOutput != "fg\n" {
		t.Fatalf("later line buf=%q capture=%q, want only fg", buf.String(), res.CapturedOutput)
	}
}

// A background job and the foreground writing at once go through the line's
// writers one write at a time (run with -race), and each echo is one
// write, as a bash builtin's output is: an echo wrote its words and its
// newline apart, and the other's line could land between them ("bg10fg6").
func TestExecute_BackgroundAndForegroundWritesAreSerialized(t *testing.T) {
	for range 1000 {
		res, err := New().Execute(context.Background(),
			`(for i in 1 2 3 4 5 6 7 8 9 10; do echo bg$i; done) & for i in 1 2 3 4 5 6 7 8 9 10; do echo fg$i; done; wait`,
			io.Discard, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(res.CapturedOutput, "\n"), "\n")
		for _, line := range lines {
			if !strings.HasPrefix(line, "bg") && !strings.HasPrefix(line, "fg") || len(line) > 4 {
				t.Fatalf("capture %q has the broken line %q", res.CapturedOutput, line)
			}
		}
		if len(lines) != 20 {
			t.Fatalf("capture %q has %d lines, want 20", res.CapturedOutput, len(lines))
		}
	}
}

// A background job's refusal never reaches the shell's refusal handler,
// which draws for the line the user is running: the job writes a plain
// message to its own stderr instead.
func TestExecute_BackgroundRefusalSkipsTheHandler(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	e := New()
	var calls atomic.Int32
	e.SetRefusalHandler(func(error) { calls.Add(1) })
	if _, err := e.Execute(context.Background(), `(sleep 0.1; hash_no_such_cmd_xyz) &`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), `sleep 0.4`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	os.Stderr = origStderr
	w.Close()
	msg, _ := io.ReadAll(r)
	if n := calls.Load(); n != 0 {
		t.Fatalf("refusal handler called %d times for a background job, want 0", n)
	}
	if !strings.Contains(string(msg), "hash: hash_no_such_cmd_xyz: command not found") {
		t.Fatalf("background refusal message %q, want a plain hash: message", msg)
	}
}

// A job is a job by the context hash runs it on, nothing the job can see
// or change: unsetting the old marker variable no longer turned a job into
// the foreground (which got a PTY, and the terminal's keys), and no marker
// shows in `set` or reaches a child under `set -a`.
func TestExecute_BackgroundJobCannotLeaveTheBackground(t *testing.T) {
	t.Parallel()
	rec := filepath.Join(t.TempDir(), "rec")
	probe := `sh -c '[ -t 0 ] && echo in-tty > ` + rec + ` || echo in-notty > ` + rec + `'`
	ttyShellAt(t, New(), "{ unset __hash_background_job; "+probe+"; } & wait")
	if data, _ := os.ReadFile(rec); strings.TrimSpace(string(data)) != "in-notty" {
		t.Errorf("the job's command had stdin %q, want in-notty: it ran as the foreground", data)
	}
	e := New()
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `set -a; { set | grep -c __hash; env | grep -c __hash; } & wait`, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(out.String()); len(got) != 2 || got[0] != "0" || got[1] != "0" {
		t.Errorf("a job saw hash's marker: set and env grep -c = %q, want 0 0", out.String())
	}
}

// A background job keeps writing while its line returns. The line read its
// capture buffer (Result.CapturedOutput, the truncation check) before the
// deferred release repointed the job, so the job wrote into the buffer as it
// was read: a data race under -race.
func TestExecute_BackgroundJobDoesNotRaceTheLineCapture(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	origStdout := os.Stdout
	os.Stdout = devnull // where the jobs write once their line ended
	defer func() { os.Stdout = origStdout }()

	e := New()
	for i := 0; i < 20; i++ {
		if _, err := e.Execute(context.Background(), `(i=0; while [ $i -lt 2000 ]; do echo x; i=$((i+1)); done) &`, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Execute(context.Background(), `wait`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}

// LetJobsStart lets a job started just before it start its command, and
// returns after its grace while a job still runs.
func TestExecutor_LetJobsStart(t *testing.T) {
	out := filepath.Join(t.TempDir(), "started")
	e := New()
	if _, err := e.Execute(context.Background(), `touch '`+out+`' &`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	e.LetJobsStart(time.Second)
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("the job's command had not run by the end of LetJobsStart: %v", err)
	}

	if _, err := e.Execute(context.Background(), `sleep 1 &`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	// The wait LetJobsStart leaves behind reads os.Stdin, which later tests
	// replace: it must be over before the test ends.
	t.Cleanup(func() { _, _ = e.Execute(context.Background(), `builtin wait`, io.Discard, io.Discard) })
	start := time.Now()
	e.LetJobsStart(100 * time.Millisecond)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("LetJobsStart(100ms) took %v with a job running", took)
	}
}

// A job whose command line redirects its stdin from the terminal itself
// (`cmd < /dev/tty &`) keeps it, as in bash, which takes only the stdin a
// job inherits from the shell away (to /dev/null). hash took any terminal
// stdin away.
func TestExecute_BackgroundJobKeepsAnExplicitTerminalRedirect(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	// The master stays open: once it is collected and closed, an open of
	// the terminal blocks.
	defer ptmx.Close()
	defer tty.Close()
	rec := filepath.Join(t.TempDir(), "rec")
	probe := `sh -c 'if [ -t 0 ]; then echo in-tty; else echo in-notty; fi > ` + rec + `'`
	for _, line := range []string{probe + " < " + tty.Name() + " & wait", "{ " + probe + " < " + tty.Name() + "; } & wait"} {
		_ = os.Remove(rec)
		if _, err := New().Execute(context.Background(), line, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(rec); strings.TrimSpace(string(data)) != "in-tty" {
			t.Errorf("%q: the job's stdin was %q, want the terminal it named", line, data)
		}
	}
}
