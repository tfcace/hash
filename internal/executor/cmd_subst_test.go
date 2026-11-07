package executor

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// A job started in a command substitution writes into the substitution:
// bash reads the substitution until every writer closed it, so it holds
// the line until the job ended ("now late"). The job used to write into
// the substitution's buffer after the line read it: a data race (-race).
func TestExecute_CommandSubstitutionWaitsForItsJobs(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		`y=$( { sleep 0.2; echo late; } & echo now); echo "[$y]"`,
		`y=$(sh -c 'sleep 0.2; echo late' & echo now); echo "[$y]"`,
		`y=$(false; true & false); echo "st=$? [$y]"`,
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out bytes.Buffer
		res, err := New().Execute(ctx, line, &out, io.Discard)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if res.JobsDone != nil {
			<-res.JobsDone
		}
		want := "[now\nlate]\n"
		if strings.HasPrefix(line, "y=$(false") {
			want = "st=1 []\n" // the substitution's status is its last command's
		}
		if out.String() != want {
			t.Errorf("%q: %q, want %q", line, out.String(), want)
		}
	}
	// $(< file) is no command: it stays the file's text.
	f := t.TempDir() + "/f"
	var out bytes.Buffer
	if _, err := New().Execute(context.Background(), `printf 'x\n' > `+f+`; echo "[$(< `+f+`)]"`, &out, io.Discard); err != nil || out.String() != "[x]\n" {
		t.Fatalf("$(< file) = %q, %v; want [x]", out.String(), err)
	}
}

// A command substitution waits only for the jobs that still hold its
// output, as bash reads until every writer closed it: one whose stdout
// (and stderr, when it is the substitution's) went elsewhere does not
// (`pid=$(server >log 2>&1 & echo $!)`), as in bash 5.2. It waited for
// every job the substitution started.
func TestExecute_CommandSubstitutionWaitsOnlyForJobsHoldingItsOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line string
		wait bool
	}{
		{`x=$(sleep 1 >/dev/null 2>&1 & echo $!)`, false},
		{`x=$(sleep 1 >/dev/null & echo $!)`, false},
		{`x=$(sleep 1 2>/dev/null & echo $!)`, true},
		{`x=$({ sleep 1; } >/dev/null 2>&1 & echo $!)`, false},
		{`x=$(sleep 1 &>/dev/null & echo $!)`, false},
		{`x=$(sleep 1 >/dev/null 2>&1 </dev/null & echo $!)`, false},
		{`x=$(sleep 1 2>&1 >/dev/null & echo $!)`, true},
		{`x=$( { sleep 1 >/dev/null 2>&1 & } 2>&1; echo $!)`, false},
		{`x=$( (sleep 1 >/dev/null) & echo $!)`, true},
	}
	for _, tc := range tests {
		e := New()
		start := time.Now()
		res, err := e.Execute(context.Background(), tc.line, io.Discard, io.Discard)
		took := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if waited := took > 700*time.Millisecond; waited != tc.wait {
			t.Errorf("%q: took %v; want a wait for the job %v", tc.line, took, tc.wait)
		}
		if res.JobsDone != nil {
			<-res.JobsDone
		}
	}
}

// heldStreams follows a statement's redirections as bash applies them, the
// descriptors the interpreter does not open too (3>&1).
func TestHeldStreams(t *testing.T) {
	tests := []struct {
		stmt           string
		stdout, stderr bool
	}{
		{"cmd", true, true},
		{"cmd >log 2>&1", false, false},
		{"cmd 2>&1 >log", true, false},
		{"cmd 2>/dev/null", true, false},
		{"cmd &>log", false, false},
		{"cmd >&log", false, false},
		{"cmd 3>&1 >log 2>&-", true, false},
		{"cmd 3>&1 >log 2>&- 3>&-", false, false},
		{"cmd >log 2>&$fd", true, true},
		{"cmd <in >log", false, true},
	}
	for _, tc := range tests {
		prog, err := syntax.NewParser().Parse(strings.NewReader(tc.stmt), "")
		if err != nil {
			t.Fatal(err)
		}
		if stdout, stderr := heldStreams(prog.Stmts[0].Redirs); stdout != tc.stdout || stderr != tc.stderr {
			t.Errorf("%q: holds stdout %v, stderr %v; want %v, %v", tc.stmt, stdout, stderr, tc.stdout, tc.stderr)
		}
	}
}
