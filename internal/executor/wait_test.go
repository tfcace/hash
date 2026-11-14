package executor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startRecordedJob starts `sh -c 'echo $$ > pidfile; body' &` on a context
// of its own and returns the job command's pid once it wrote it; the
// command is killed when the test ends.
func startRecordedJob(t *testing.T, e *Executor, body string) int {
	t.Helper()
	pidf := filepath.Join(t.TempDir(), "pid")
	if _, err := e.Execute(context.Background(), "sh -c 'echo $$ > "+pidf+"; "+body+"' &", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(pidf)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the job never started its command")
	return 0
}

// wait ends with its line, as in bash 5.2, where Ctrl+C at wait (the
// line's context ending) ends it at once: the interpreter's wait blocked
// until the job ended, a stopped job's never.
func TestExecute_WaitEndsWithItsLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, job, wait string }{
		{"wait for all", "sleep 5", "wait"},
		{"wait for the last job", "sleep 5", "wait $!"},
		{"builtin wait", "sleep 5", "builtin wait"},
		{"a stopped job", "kill -STOP $$", "wait"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			startRecordedJob(t, e, tc.job)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			done := make(chan time.Duration, 1)
			start := time.Now()
			go func() {
				_, _ = e.Execute(ctx, tc.wait, io.Discard, io.Discard)
				done <- time.Since(start)
			}()
			select {
			case took := <-done:
				if took > 2*time.Second {
					t.Errorf("%q took %v after its line ended", tc.wait, took)
				}
			case <-time.After(4 * time.Second):
				t.Fatalf("%q still blocked 4s after its line ended", tc.wait)
			}
		})
	}
}

// A wait whose line goes on waits for the job and takes its status.
func TestExecute_WaitTakesTheJobsStatus(t *testing.T) {
	e := New()
	res, err := e.Execute(context.Background(), "sh -c 'sleep 0.1; exit 3' & wait $!; echo $?; wait; echo $?", nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.CapturedOutput != "3\n0\n" {
		t.Errorf("output %q, want 3 then 0", res.CapturedOutput)
	}
	res, _ = e.Execute(context.Background(), "wait g99", nil, io.Discard)
	if res.ExitCode == 0 {
		t.Error("wait for a job that is not there succeeded")
	}
}

// wait -n waits for the first of the shell's running jobs to end, or of
// the jobs it names, and takes its status; with none running it fails
// with 127, as bash's. The interpreter has no -n ("invalid option").
func TestExecute_WaitNTakesTheFirstJobToEnd(t *testing.T) {
	t.Parallel()
	tests := []struct{ line, want string }{
		{`wait -n; echo "none=$?"`, "none=127\n"},
		{`(sleep 0.4; exit 3) & (sleep 0.1; exit 4) & wait -n; echo "n=$?"; wait -n; echo "n=$?"; wait -n; echo "n=$?"`, "n=4\nn=3\nn=127\n"},
		{`(sleep 0.3; exit 3) & (sleep 0.1; exit 4) & wait -n %1; echo "named=$?"; wait`, "named=3\n"},
		{`(exit 5) & sleep 0.1; wait -n $!; echo "ended=$?"`, "ended=5\n"},
		{`wait -n %9 2>&1; echo "st=$?"`, "hash: wait: %9: no such job\nst=127\n"},
		{`sleep 5 & p=$(jobs -p); (sleep 0.1; kill $p) & builtin wait -n %1; echo "st=$?"`, "st=143\n"},
	}
	runLines(t, tests)
}

// wait -n takes a job that ended before it and that no wait took yet, the
// lowest number first, as bash 5.2's: a wait that named the job, or wait
// -n, took it; a bare wait did not. A subshell has none of its shell's.
func TestExecute_WaitNTakesAJobThatEndedBeforeIt(t *testing.T) {
	t.Parallel()
	runLines(t, []struct{ line, want string }{
		{`(exit 3) & sleep 0.3; wait -n; echo "n=$?"; wait -n; echo "n=$?"`, "n=3\nn=127\n"},
		{`(exit 3) & (exit 4) & sleep 0.3; wait -n; echo "n=$?"; wait -n; echo "n=$?"`, "n=3\nn=4\n"},
		{`(exit 3) & sleep 5 & sleep 0.3; wait -n; echo "n=$?"; kill %2`, "n=3\n"},
		{`(exit 3) & p=$!; sleep 0.3; wait $p; wait -n; echo "n=$?"`, "n=127\n"},
		{`(exit 3) & sleep 0.3; wait %1; wait -n; echo "n=$?"`, "n=127\n"},
		{`(exit 3) & sleep 0.3; wait; wait -n; echo "n=$?"`, "n=3\n"},
		{`(exit 3) & sleep 0.3; (wait -n; echo "sub=$?"); wait -n; echo "n=$?"`, "sub=127\nn=3\n"},
	})
}

// runLines runs each test's line in a new shell and checks its stdout.
func runLines(t *testing.T, tests []struct{ line, want string }) {
	t.Helper()
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb strings.Builder
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want {
			t.Errorf("%q: out %q, stderr %q, err %v; want %q", tc.line, out.String(), errb.String(), err, tc.want)
		}
	}
}

// wait takes the pids jobs -p prints, as bash's: it waits for the job
// that is that one command and takes its status, the job running or ended
// (`for p in $(jobs -p); do wait "$p"; done`), and wait -n takes them too.
func TestExecute_WaitTakesThePidsJobsPPrints(t *testing.T) {
	t.Parallel()
	tests := []struct{ line, want string }{
		{`sh -c 'sleep 0.3; exit 3' & sleep 0.4 & for p in $(jobs -p); do wait "$p"; echo "st=$?"; done`, "st=3\nst=0\n"},
		{`sleep 0.4 & sh -c 'sleep 0.1; exit 3' & for p in $(jobs -p); do wait "$p"; echo "st=$?"; done`, "st=0\nst=3\n"},
		{`sh -c 'sleep 0.2; exit 4' & p=$(jobs -p); wait -n "$p"; echo "n=$?"`, "n=4\n"},
	}
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb strings.Builder
		start := time.Now()
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want || time.Since(start) < 200*time.Millisecond {
			t.Errorf("%q: out %q, stderr %q, err %v after %v; want %q once the jobs ended", tc.line, out.String(), errb.String(), err, time.Since(start), tc.want)
		}
	}
}

// wait for an operand that names no job of the shell's fails with 127 and
// bash's words, as wait -n does: `%3: no such job`, `pid N is not a child
// of this shell`; one that is neither a pid nor a job spec with 1 (and
// wait -n takes it for a job spec, with 127). builtin wait and command
// wait fail as wait.
func TestExecute_WaitForNoJobFailsWith127(t *testing.T) {
	tests := []struct{ line, want string }{
		{`wait %3; echo "a=$?"; wait 99999; echo "b=$?"`, "a=127\nb=127\n"},
		{`wait %3 2>&1; wait -n %3 2>&1`, "hash: wait: %3: no such job\nhash: wait: %3: no such job\n"},
		{`wait 99999 2>&1; wait -n 99999 2>&1`, "hash: wait: pid 99999 is not a child of this shell\nhash: wait: pid 99999 is not a child of this shell\n"},
		{`wait abc 2>&1; echo "c=$?"`, "hash: wait: `abc': not a pid or valid job spec\nc=1\n"},
		{`wait -n abc 2>&1; echo "c=$?"`, "hash: wait: abc: no such job\nc=127\n"},
		{`builtin wait -n; echo "b=$?"; command wait -n; echo "c=$?"; builtin wait %2; echo "d=$?"`, "b=127\nc=127\nd=127\n"},
	}
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb strings.Builder
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want {
			t.Errorf("%q: out %q, stderr %q, err %v; want %q", tc.line, out.String(), errb.String(), err, tc.want)
		}
	}
}
