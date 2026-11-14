package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/interp"
)

// A line says when the last background job it started ended (JobsDone):
// nil when none runs as it returns, else a channel closed once the last
// one, its own or one a job of it started, ended. The guess it made
// before (MayStartJobs) missed jobs started through a variable or an alias
// and kept lines that started none.
func TestExecute_JobsDone(t *testing.T) {
	t.Parallel()
	e := New()
	ctx := context.Background()
	if _, err := e.Execute(ctx, "f() { sleep 0.2 & }; alias g='sleep 0.2 &'", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		line string
		jobs bool
	}{
		{"true", false},
		{"h() { :; }", false},
		{"echo hi | cat", false},
		{"sleep 0 & wait", false}, // its job ended before it returned
		{"sleep 0.2 &", true},
		{"{ true; sleep 0.2 & }", true},
		{"x=f; $x", true},
		{"g", true},
		{"eval 'sleep 0.2 &'", true},
		{"(sleep 0.2 &)", true}, // a subshell's job runs on the line too
		{"{ sleep 0.05; sleep 0.2 & } &", true},
		{"if sleep 0.2 & then :; fi", true},
		{"for i in 1; do sleep 0.2 & done", true},
		{"echo $(sleep 0.2 &)", false}, // the substitution holds the line until its job ended, as in bash
	} {
		start := time.Now()
		res, err := e.Execute(ctx, tt.line, io.Discard, io.Discard)
		if err != nil {
			t.Fatalf("%q: %v", tt.line, err)
		}
		if got := res.JobsDone != nil; got != tt.jobs {
			t.Errorf("%q: JobsDone set = %v, want %v", tt.line, got, tt.jobs)
			continue
		}
		if res.JobsDone == nil {
			continue
		}
		select {
		case <-res.JobsDone:
			if took := time.Since(start); took < 150*time.Millisecond {
				t.Errorf("%q: JobsDone closed after %v, before the job ended", tt.line, took)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%q: JobsDone never closed", tt.line)
		}
	}
}

// A job started inside a job after its line returned keeps that line's
// JobsDone open until it ended too.
func TestExecute_JobsDoneWaitsForTheJobsOfAJob(t *testing.T) {
	t.Parallel()
	e := New()
	out := filepath.Join(t.TempDir(), "out")
	res, err := e.Execute(context.Background(), "{ sleep 0.1; { sleep 0.3; echo inner > "+out+"; } & } &", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-res.JobsDone:
	case <-time.After(5 * time.Second):
		t.Fatal("JobsDone never closed")
	}
	if data, _ := os.ReadFile(out); strings.TrimSpace(string(data)) != "inner" {
		t.Errorf("JobsDone closed before the inner job ended (out %q)", data)
	}
}

// The table's call after a `&` leaves $? at 0 and $! naming the job, as
// the `&` left them.
func TestExecute_JobTableCallKeepsStatusAndJobName(t *testing.T) {
	e := New()
	res, err := e.Execute(context.Background(), "false; sleep 0 & echo \"$? $!\"; wait", nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.CapturedOutput != "0 g1\n" {
		t.Errorf("output %q, want 0 g1", res.CapturedOutput)
	}
}

// A job runs on when Ctrl+C ends the line that started it (the line's
// context ends), as a bash job does: it ran on that context, so the
// interrupt cut off its in-process part (`{ sleep 1; echo hi; } & sleep
// 10`, Ctrl+C: hi never came). The line's own work still stops.
func TestExecute_JobOutlivesItsLinesInterrupt(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "out")
	e := New()
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(200*time.Millisecond, func() { cancel(ErrLineInterrupted) }) // Ctrl+C at the sleep
	start := time.Now()
	res, err := e.Execute(ctx, "{ sleep 0.4; echo job > "+out+"; } & sleep 5; echo line >> "+out, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the line took %v after its context ended", took)
	}
	if res.JobsDone == nil {
		t.Fatal("the line's job runs, but JobsDone is nil")
	}
	select {
	case <-res.JobsDone:
	case <-time.After(5 * time.Second):
		t.Fatal("JobsDone never closed")
	}
	if data, _ := os.ReadFile(out); string(data) != "job\n" {
		t.Errorf("out = %q, want the job's line and not the interrupted line's", data)
	}
}

// A line's context that ends for another cause than Ctrl+C (the shell
// ending) ends its jobs' own parts too; and so does the shell ending its
// jobs (SignalJobs), for the jobs of a line Ctrl+C ended before.
func TestExecute_JobEndsWithTheShell(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		end  func(e *Executor, cancel context.CancelCauseFunc)
	}{
		{"the line's context ends", func(_ *Executor, cancel context.CancelCauseFunc) { cancel(nil) }},
		{"the shell ends its jobs", func(e *Executor, cancel context.CancelCauseFunc) {
			cancel(ErrLineInterrupted)
			e.SignalJobs(syscall.SIGHUP)
		}},
	} {
		out := filepath.Join(t.TempDir(), "out")
		e := New()
		ctx, cancel := context.WithCancelCause(context.Background())
		res, err := e.Execute(ctx, "{ sleep 0.3; echo job > "+out+"; } &", io.Discard, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		tc.end(e, cancel)
		select {
		case <-res.JobsDone:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: JobsDone never closed", tc.name)
		}
		time.Sleep(400 * time.Millisecond)
		if _, err := os.Stat(out); err == nil {
			t.Errorf("%s: the job's own part ran on", tc.name)
		}
	}
}

// A line's jobs go from running with the line, to outliving it (a channel
// that closes as the last ends), to all ended; a line that returns with
// none running skips the middle. A job that ended but whose end has not
// arrived yet counts as ended at the return.
func TestLineJobs_States(t *testing.T) {
	var j lineJobs
	a, b := make(chan struct{}), make(chan struct{})
	j.start(a)
	j.start(b)
	j.end(a)
	if j.state != lineJobsLineRuns {
		t.Fatalf("state %v while the line runs", j.state)
	}
	done := j.lineReturned()
	if done == nil || j.state != lineJobsOutlive {
		t.Fatalf("returned with b running: done %v, state %v; want a channel, outlive", done, j.state)
	}
	c := make(chan struct{}) // b's own job
	j.start(c)
	j.end(b)
	select {
	case <-done:
		t.Fatal("done closed with c running")
	default:
	}
	j.end(c)
	select {
	case <-done:
	default:
		t.Fatal("done open after the last job ended")
	}
	if j.state != lineJobsAllEnded {
		t.Fatalf("state %v after the last job, want all ended", j.state)
	}

	var k lineJobs
	ended := make(chan struct{})
	k.start(ended)
	close(ended) // ended, its end on its way
	if done := k.lineReturned(); done != nil || k.state != lineJobsAllEnded {
		t.Fatalf("returned with its jobs ended: done %v, state %v; want nil, all ended", done, k.state)
	}
	k.end(ended) // the end arrives late: nothing to close
}

// A job reaches the shell that started it by its id: jobs of one id queue
// in order, a take waits for its job until the job ends, and a take of an
// id no job of markJobs's has waits for nothing.
func TestJobSpawner_HandsEachJobByItsID(t *testing.T) {
	var s jobSpawner
	never := make(chan struct{})
	if j := s.take(0, never); j != nil {
		t.Fatal("id 0 took a job")
	}
	if j := s.take(jobIDs.Load()+1000, never); j != nil {
		t.Fatal("an id markJobs never gave took a job")
	}
	id := jobIDs.Add(1)
	j1, j2 := &job{}, &job{}
	s.arrive(id, j1)
	s.arrive(id, j2)
	if got := s.take(id, never); got != j1 {
		t.Fatal("the first job of an id did not come first")
	}
	if got := s.take(id, never); got != j2 {
		t.Fatal("the second job of an id did not come second")
	}
	done := make(chan struct{})
	close(done)
	if got := s.take(id, done); got != nil {
		t.Fatal("take at a job's end took one that never arrived")
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		s.arrive(id, j2)
	}()
	if got := s.take(id, never); got != j2 {
		t.Fatal("take did not wait for its job to arrive")
	}
}

// The calls hash follows `&` with take an id: a user's call without one
// runs nothing and enters nothing, and returns at once.
func TestExecute_UsersJobCallsRunNothing(t *testing.T) {
	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := New().Execute(ctx, `__hash_job_run 'echo ran'; echo st=$?; builtin eval 'sleep 2 &'; __hash_job_started; __hash_job_started 999999999; echo done`, &out, &errb)
	if err != nil || out.String() != "st=2\ndone\n" || res.ExitCode != 0 {
		t.Fatalf("out %q, stderr %q, err %v; want st=2, then done at once", out.String(), errb.String(), err)
	}
}

// jobs lists the calling shell's running jobs as bash's builtin does, -l
// with the pids of their commands, -p only those; a subshell that started
// none lists the shell's.
func TestExecute_JobsListsTheShellsJobs(t *testing.T) {
	t.Parallel()
	e := New()
	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := e.Execute(ctx, `sleep 5 & sleep 5 | cat & sleep 0.2; jobs; jobs -p | wc -l | tr -d ' '; (jobs; echo sub); kill %1 %2; wait`, &out, &errb)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("err %v, status %d, stderr %q", err, res.ExitCode, errb.String())
	}
	listing := "[1]-  Running                 sleep 5 &\n[2]+  Running                 sleep 5 | cat &\n"
	want := listing + "2\n" + listing + "sub\n"
	if out.String() != want {
		t.Fatalf("out %q, want %q", out.String(), want)
	}
	out.Reset()
	if _, err := e.Execute(ctx, "jobs -x", &out, &errb); err != nil || !strings.Contains(errb.String(), "jobs: -x: invalid option") {
		t.Fatalf("jobs -x: stderr %q, err %v", errb.String(), err)
	}
}

// The interactive shell says `[N] pid` as a job it started starts, and
// queues `[N]+  Done ...` (Exit N, the signal that ended it) for its next
// prompt as the job ends, as bash -i does; a script says nothing.
func TestExecute_InteractiveShellTellsOfItsJobs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line, notice string
	}{
		{"sleep 0.1 &", "[1]+  Done                    sleep 0.1 &"},
		{"sh -c 'exit 3' &", "[1]+  Exit 3                  sh -c 'exit 3' &"},
		{"sleep 5 & kill %1", "[1]+  Terminated              sleep 5 &"},
		{"sh -c 'kill -9 $$' &", "[1]+  Killed                  sh -c 'kill -9 $$' &"},
		{"{ true\ntrue; } &", "[1]+  Done                    { true; true; } &"},
	} {
		for _, mode := range []terminalMode{terminalInteractive, terminalPlain} {
			e := New()
			e.terminal = mode
			var errb bytes.Buffer
			res, err := e.Execute(context.Background(), tc.line, io.Discard, &errb)
			if err != nil {
				t.Fatal(err)
			}
			if res.JobsDone != nil {
				<-res.JobsDone
			}
			time.Sleep(20 * time.Millisecond) // the notice follows the job's end
			notices := e.JobNotices()
			if mode == terminalPlain {
				if errb.Len() != 0 || len(notices) != 0 {
					t.Errorf("script %q: stderr %q, notices %q; want none", tc.line, errb.String(), notices)
				}
				continue
			}
			if start := errb.String(); !strings.HasPrefix(start, "[1] ") || strings.Count(start, "\n") != 1 {
				t.Errorf("%q: start notice %q, want [1] and a pid", tc.line, start)
			}
			if len(notices) != 1 || notices[0] != tc.notice {
				t.Errorf("%q: notices %q, want %q", tc.line, notices, tc.notice)
			}
		}
	}
}

// jobs -p names each running job as kill takes it, right after its &: the
// pid of a job's one command once it started (waiting for its start as the
// start notice does), else its gN, which ends the whole job (shell code, a
// { ...; } block, a pipeline, a function), where one pid would end only
// the command running then. It listed only the pids of commands that had
// started, so right after & it listed nothing (`sleep 5 & kill $(jobs -p)`
// was a kill usage error), and nothing for a job of shell code.
func TestExecute_JobsPNamesEachJobForKill(t *testing.T) {
	tests := []struct{ line, want string }{
		{`sleep 5 & jobs -p | grep -cx '[0-9][0-9]*'; kill $(jobs -p); echo "k=$?"; wait %1; echo "st=$?"`, "1\nk=0\nst=143\n"},
		{`sleep 5 & jobs -l | grep -c '^\[1\]+ [0-9][0-9]* Running'; kill %1`, "1\n"},
		{`{ sleep 5; echo never; } & jobs -p; kill $(jobs -p); wait %1; echo "st=$?"`, "g1\nst=143\n"},
		{`f() { sleep 5; echo never; }; f & jobs -p; kill $(jobs -p); wait %1; echo "st=$?"`, "g1\nst=143\n"},
		{`sleep 5 | cat & jobs -p; kill $(jobs -p); wait %1; echo "st=$?"`, "g1\nst=143\n"},
		{`while :; do :; done & jobs -p; kill $(jobs -p); wait %1; echo "st=$?"`, "g1\nst=143\n"},
	}
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb bytes.Buffer
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want {
			t.Errorf("%q: out %q, stderr %q, err %v; want %q", tc.line, out.String(), errb.String(), err, tc.want)
		}
	}
}

// The cleanup idiom `trap 'kill $(jobs -p)' EXIT` ends a job the script
// started just before it exited: jobs -p listed nothing for a job whose
// command had not started yet, and the command started after the trap and
// outlived the script.
func TestExecute_ExitTrapKillsTheJobsJobsPNames(t *testing.T) {
	const marker = "31.4159"
	defer func() { _ = exec.Command("pkill", "-f", "sleep "+marker).Run() }()
	e := New()
	var errb bytes.Buffer
	if _, err := e.Execute(context.Background(), `trap 'kill $(jobs -p)' EXIT; sleep `+marker+` &`, io.Discard, &errb); err != nil {
		t.Fatal(err)
	}
	e.RunExitTrap(context.Background(), io.Discard, &errb)
	e.LetJobsStart(100 * time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	if out, err := exec.Command("pgrep", "-f", "sleep "+marker).Output(); err == nil {
		t.Errorf("the job's sleep outlived the EXIT trap's kill $(jobs -p): pids %q, stderr %q", out, errb.String())
	}
}

// A job goes from jobStarting to jobRunning as its first command starts,
// and to jobEnded as its part ends, closing running as it leaves
// jobStarting; kill marks it in either phase before jobEnded, and the end
// notice words what it learned (endState).
func TestJob_States(t *testing.T) {
	newJob := func() (*job, *int) {
		cancels := 0
		return &job{running: make(chan struct{}), cancel: func(error) { cancels++ }}, &cancels
	}
	isClosed := func(c chan struct{}) bool {
		select {
		case <-c:
			return true
		default:
			return false
		}
	}
	started := func(t *testing.T, args ...string) *exec.Cmd {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}

	t.Run("shell code only", func(t *testing.T) {
		j, _ := newJob()
		if j.state.phase != jobStarting || isClosed(j.running) {
			t.Fatalf("new job: phase %d, running closed %v", j.state.phase, isClosed(j.running))
		}
		j.partEnded(0)
		if j.state.phase != jobEnded || !isClosed(j.running) || j.endState() != "Done" {
			t.Fatalf("ended: phase %d, running closed %v, %q", j.state.phase, isClosed(j.running), j.endState())
		}
		j.kill(syscall.SIGTERM) // too late: it stays Done
		if j.state.killed != 0 || j.endState() != "Done" {
			t.Fatalf("kill after the end marked it %v (%q)", j.state.killed, j.endState())
		}
	})

	t.Run("killed before its command started", func(t *testing.T) {
		j, cancels := newJob()
		j.kill(syscall.SIGTERM)
		j.kill(syscall.SIGKILL) // the first kill's signal stays
		if j.state.killed != syscall.SIGTERM || *cancels != 1 || j.state.phase != jobStarting {
			t.Fatalf("killed %v, cancels %d, phase %d", j.state.killed, *cancels, j.state.phase)
		}
		cmd := started(t, "sleep", "5")
		if sig := j.startCommand(cmd); sig != syscall.SIGTERM {
			t.Errorf("startCommand reported %v, want the kill's SIGTERM", sig)
		}
		if j.state.phase != jobRunning || !isClosed(j.running) {
			t.Errorf("phase %d, running closed %v; want jobRunning", j.state.phase, isClosed(j.running))
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		j.endCommand(cmd)
		j.partEnded(143)
		if got := j.endState(); got != "Terminated" {
			t.Errorf("endState %q, want Terminated", got)
		}
	})

	t.Run("its last command died of a signal", func(t *testing.T) {
		j, _ := newJob()
		cmd := started(t, "sh", "-c", "kill -9 $$")
		j.startCommand(cmd)
		_ = cmd.Wait()
		j.endCommand(cmd)
		j.partEnded(137)
		if got := j.endState(); got != "Killed" {
			t.Errorf("endState %q, want Killed", got)
		}
	})

	t.Run("exit status", func(t *testing.T) {
		j, _ := newJob()
		cmd := started(t, "sh", "-c", "exit 3")
		j.startCommand(cmd)
		_ = cmd.Wait()
		j.endCommand(cmd)
		j.partEnded(3)
		if got := j.endState(); got != "Exit 3" {
			t.Errorf("endState %q, want Exit 3", got)
		}
	})
}

// The running jobs (shellJobs: what jobs lists, what %% and wait -n take)
// leave out a job whose part ended, though the interpreter has not closed
// its channel yet, nor the table dropped it: `wait -n` took a job it had
// just waited for a second time (n=3, then n=3 again, not 127). The end
// notice still marks the job among the jobs that ran with it.
func TestJobTable_RunningJobsLeaveOutEndedOnes(t *testing.T) {
	shell := &interp.Runner{}
	var tbl jobTable
	jobs := make([]*job, 2)
	for i := range jobs {
		jobs[i] = &job{running: make(chan struct{}), cancel: func(error) {}, listed: jobListing{n: i + 1, shell: shell}}
		if !tbl.add(make(chan struct{}), jobs[i]) {
			t.Fatal("add refused a running job")
		}
	}
	jobs[0].partEnded(0)
	if got := tbl.shellJobs(shell); len(got) != 1 || got[0] != jobs[1] {
		t.Errorf("running jobs %v, want only job 2", got)
	}
	if mark := tbl.mark(jobs[0]); mark != '-' {
		t.Errorf("the ended job 1's mark %q, want - (job 2 runs on)", mark)
	}
}

// jobs takes bash's combined options (-lp, -p wins as in bash) and -r
// (every job runs), and lists only the jobs its operands name, failing
// with 1 for one that names none; builtin jobs and command jobs are jobs.
func TestExecute_JobsTakesOptionsAndJobSpecs(t *testing.T) {
	tests := []struct{ line, want string }{
		{`sleep 5 & jobs -lp | grep -cx '[0-9][0-9]*'; kill %1`, "1\n"},
		{`sleep 5 & sleep 5 & jobs %1; jobs -r %+; kill %1 %2`, "[1]-  Running                 sleep 5 &\n[2]+  Running                 sleep 5 &\n"},
		{`jobs %3 2>&1; echo "st=$?"`, "hash: jobs: %3: no such job\nst=1\n"},
		{`sleep 5 & builtin jobs; command jobs -p | wc -l | tr -d ' '; kill %1`, "[1]+  Running                 sleep 5 &\n1\n"},
		{`jobs -s; echo "st=$?"`, "st=0\n"},
	}
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb bytes.Buffer
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want {
			t.Errorf("%q: out %q, stderr %q, err %v; want %q", tc.line, out.String(), errb.String(), err, tc.want)
		}
	}
}

// A job stays in the table as it ends, its one record ended (jobEnded):
// out of the running ones (shellJobs, lookup, running), and found by the
// pid of the one command it was (pidJob), as wait takes it. A job that
// ended before its shell entered it is entered ended.
func TestJobTable_KeepsAnEndedJobsRecord(t *testing.T) {
	shell := &interp.Runner{}
	var tbl jobTable
	newJob := func(n, pid int) *job {
		return &job{running: make(chan struct{}), cancel: func(error) {}, command: "sleep", pids: []int{pid}, listed: jobListing{n: n, shell: shell}}
	}
	done1, done2 := make(chan struct{}), make(chan struct{})
	j1, j2 := newJob(1, 101), newJob(2, 102)
	if !tbl.add(done1, j1) {
		t.Fatal("add refused a running job")
	}
	j1.partEnded(0)
	close(done1)
	close(done2)
	j2.partEnded(3)
	if tbl.add(done2, j2) {
		t.Error("add took a job that ended for a running one")
	}
	if tbl.add(done2, j2) {
		t.Error("add entered a job twice")
	}
	if got := tbl.shellJobs(shell); len(got) != 0 {
		t.Errorf("running jobs %v, want none", got)
	}
	if tbl.lookup(done1) != nil || len(tbl.running()) != 0 {
		t.Error("an ended job is among the running ones")
	}
	if n1, n2, none := tbl.pidJob(shell, 101), tbl.pidJob(shell, 102), tbl.pidJob(&interp.Runner{}, 101); n1 != 1 || n2 != 2 || none != 0 {
		t.Errorf("pidJob 101 %d, 102 %d, another shell's %d; want 1, 2, 0", n1, n2, none)
	}
}

// wait -n takes a shell's jobs that run and the ones that ended and that
// no wait took (unwaited): once a wait took one (jobWaited), no more; a
// disowned job, never.
func TestJobTable_UnwaitedJobsUntilAWaitTookThem(t *testing.T) {
	shell := &interp.Runner{}
	var tbl jobTable
	dones := make([]chan struct{}, 3)
	jobs := make([]*job, 3)
	for i := range jobs {
		dones[i] = make(chan struct{})
		jobs[i] = &job{running: make(chan struct{}), cancel: func(error) {}, listed: jobListing{n: i + 1, shell: shell}}
		tbl.add(dones[i], jobs[i])
	}
	jobs[0].partEnded(3)
	close(dones[0])
	jobs[2].disown(false)
	if got := tbl.unwaited(shell); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("unwaited %v, want [1 2]", got)
	}
	tbl.waited(dones[0])
	if got := tbl.unwaited(shell); !slices.Equal(got, []int{2}) {
		t.Errorf("unwaited %v once job 1 was waited for, want [2]", got)
	}
	if jobs[0].phase() != jobWaited {
		t.Errorf("job 1's phase %d, want jobWaited", jobs[0].phase())
	}
}

// A background job costs what the first one did however many ended before
// it, in what it allocates and in what the shell keeps of it once it ended:
// a loop of `cmd & wait $!`, and one that starts its jobs and waits for
// them all at its end, which leaves each one's record in the table.
func TestExecute_AJobCostsTheSameHoweverManyEndedBeforeIt(t *testing.T) {
	for _, line := range []string{
		`i=0; while [ $i -lt 500 ]; do : & wait $!; i=$((i+1)); done`,
		`i=0; while [ $i -lt 500 ]; do : & i=$((i+1)); done; wait`,
	} {
		e := New()
		batch := func() (allocated, held uint64) {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			execLine(t, e, line)
			runtime.GC()
			runtime.ReadMemStats(&after)
			return after.TotalAlloc - before.TotalAlloc, after.HeapAlloc
		}
		first, heldFirst := batch()
		for range 4 {
			batch()
		}
		sixth, heldSixth := batch()
		if sixth > first*3/2 {
			t.Errorf("%s: the sixth batch of jobs allocated %d KB, the first %d KB", line, sixth>>10, first>>10)
		}
		if grown := int64(heldSixth) - int64(heldFirst); grown > 4<<20 {
			t.Errorf("%s: 2500 ended jobs hold %d KB", line, grown>>10)
		}
	}
}

// A wait that took a job's status takes the job out of the table, as bash
// forgets a job it waited for: its pid names no job after it.
func TestJobTable_AWaitedJobLeavesTheTable(t *testing.T) {
	shell := &interp.Runner{}
	var tbl jobTable
	done := make(chan struct{})
	j := &job{running: make(chan struct{}), cancel: func(error) {}, command: "sleep", pids: []int{101}, listed: jobListing{n: 1, shell: shell}}
	tbl.add(done, j)
	j.partEnded(0)
	close(done)
	if n := tbl.pidJob(shell, 101); n != 1 {
		t.Fatalf("pidJob of an ended job %d, want 1", n)
	}
	tbl.waited(done)
	if n := tbl.pidJob(shell, 101); n != 0 {
		t.Errorf("pidJob of a waited job %d, want 0", n)
	}
	if len(tbl.jobs) != 0 {
		t.Errorf("the table keeps %d jobs, want none", len(tbl.jobs))
	}
	if tbl.mark(j) != '+' {
		t.Errorf("the mark of the only job, waited for, %q, want +", tbl.mark(j))
	}
}
