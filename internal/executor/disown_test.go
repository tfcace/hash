package executor

import (
	"bytes"
	"context"
	"strings"
	"syscall"
	"testing"
	"time"
)

// disown takes a job out of the shell's jobs, as bash's: jobs lists it no
// more, %N names it no more, wait does not wait for it, and its status is
// 0. disown -h keeps it listed. A call that names no job fails with 1.
func TestExecute_DisownTakesAJobOutOfTheShellsJobs(t *testing.T) {
	tests := []struct{ line, want string }{
		{`sleep 1 & disown; echo "d=$?"; jobs | wc -l | tr -d ' '`, "d=0\n0\n"},
		{`sleep 1 & sleep 1 & disown %1; jobs`, "[2]+  Running                 sleep 1 &\n"},
		{`sleep 1 & disown -h; echo "d=$?"; jobs`, "d=0\n[1]+  Running                 sleep 1 &\n"},
		{`sleep 1 & sleep 1 & disown -a; jobs; echo done`, "done\n"},
		{`sleep 1 & builtin disown; jobs; echo done`, "done\n"},
		{`sleep 1 & disown; kill %1 2>&1; wait %1 2>&1; echo "w=$?"`, "hash: kill: %1: no such job\nhash: wait: %1: no such job\nw=127\n"},
		{`sleep 1 & disown $(jobs -p); jobs; echo done`, "done\n"},
		{`disown 2>&1; echo "a=$?"; disown %5 2>&1; echo "b=$?"; disown -x 2>&1; echo "c=$?"`,
			"hash: disown: current: no such job\na=1\nhash: disown: %5: no such job\nb=1\n" +
				"hash: disown: -x: invalid option\ndisown: usage: disown [-h] [-ar] [jobspec ... | pid ...]\nc=2\n"},
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
	// wait does not wait for a disowned job.
	start := time.Now()
	var out bytes.Buffer
	if _, err := New().Execute(context.Background(), `sleep 2 & disown; wait; echo "w=$?"`, &out, &out); err != nil || out.String() != "w=0\n" {
		t.Errorf("wait after disown: %q %v", out.String(), err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("wait waited %v for a disowned job", time.Since(start))
	}
}

// The shell's hangup passes a disowned job by, and a disown -h one, as
// bash's: their commands live on, where a job of the shell's gets it.
func TestExecute_HangupPassesADisownedJobBy(t *testing.T) {
	t.Chdir(t.TempDir())
	e := New()
	e.terminal = terminalInteractive
	var out bytes.Buffer
	line := `sh -c 'echo $$ > a; exec sleep 30' & disown; sh -c 'echo $$ > b; exec sleep 30' & disown -h; sh -c 'echo $$ > c; exec sleep 30' &`
	if _, err := e.Execute(context.Background(), line, &out, &out); err != nil {
		t.Fatal(err)
	}
	a, b, c := pidIn(t, "a"), pidIn(t, "b"), pidIn(t, "c")
	defer syscall.Kill(a, syscall.SIGKILL) //nolint:errcheck // cleanup
	defer syscall.Kill(b, syscall.SIGKILL) //nolint:errcheck // cleanup
	e.SignalJobs(syscall.SIGHUP)
	if !gone(c) {
		t.Error("the hangup left the shell's job running")
	}
	for name, pid := range map[string]int{"disowned": a, "disown -h": b} {
		if syscall.Kill(pid, 0) != nil {
			t.Errorf("the hangup ended the %s job", name)
		}
	}
}

// A job's disposition only moves away from the shell: disown -h leaves an
// owned job out of the hangup, disown takes either out of the jobs, and
// disown -h of a disowned job leaves it disowned. A job started inside a
// job the shell left is out of the hangup with it.
func TestJob_DispositionTransitions(t *testing.T) {
	for _, tc := range []struct {
		calls []bool // disown(hangupOnly) in order
		want  jobDisposition
	}{
		{nil, jobOwned},
		{[]bool{true}, jobNoHangup},
		{[]bool{false}, jobDisowned},
		{[]bool{true, false}, jobDisowned},
		{[]bool{false, true}, jobDisowned},
	} {
		j := &job{}
		for _, hangupOnly := range tc.calls {
			j.disown(hangupOnly)
		}
		if got := j.disposition(); got != tc.want {
			t.Errorf("disown calls %v: %v, want %v", tc.calls, got, tc.want)
		}
		inner := &job{parent: j}
		if inner.hangsUp() != (tc.want == jobOwned) {
			t.Errorf("disown calls %v: a job inside hangs up %v", tc.calls, inner.hangsUp())
		}
	}
}
