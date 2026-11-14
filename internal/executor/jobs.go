package executor

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tfcace/hash/internal/shparse"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// The interpreter runs a background job (`cmd &`) as a goroutine on the
// context of the line that started it, and keeps no more of it than a
// channel closed when it ends (its bgProcs, which wait reads). hash runs
// the job's statements itself, on a context of the job's own (runJobPart),
// and keeps the shell's jobs in a table as they run: each `&` the shell
// parses is followed by a call of jobStartedCommand, which the interpreter
// makes right after it started the job, on the same goroutine, and which
// enters the job in the table by its channel (Executor.jobStarted), once
// the job's part handed it over (jobSpawner). A job stays in the table
// ended (jobEnded) until a wait takes its status. The table tells
//
//   - the line that started jobs when the last of them ended (its
//     lineJobs, Result.JobsDone), so its caller can end the context they
//     run on then and not before;
//   - which commands of jobs still run, for the hangup the interactive
//     shell hands them as it ends (SignalJobs);
//   - which job $! (g1, g2, ...) or %N names, for kill (killJobs);
//   - when every job ended, for the moment a script gives its jobs to
//     start their commands as it exits (LetJobsStart).
//
// hash has no job control: it neither stops nor continues jobs. A job runs
// until it ends, kill ends it, or the shell does.

// jobStartedCommand is the call that follows each `&` hash parses, with
// the job's id (see markBackgroundJobs). A user's call of it enters nothing
// new: an id that is no job's on its way (jobSpawner) takes none.
const jobStartedCommand = "__hash_job_started"

// job is a background job while it runs: the context its own part runs on,
// which kill ends, and the commands it started that still run.
type job struct {
	parent *job // the job that started it, nil for the foreground
	// substOut are the command substitutions' writers its statement holds
	// open as it runs (heldStreams): a substitution waits for it at its end
	// only when its writer is one (substEnd).
	substOut []*lockedWriter
	line     context.Context // the context of the line it belongs to
	// cancel ends the context its own part runs on (detach); nil once the
	// part ended (partEnded), so an ended job's record keeps none of what
	// that context holds: the job's subshell and its copy of the variables.
	cancel context.CancelCauseFunc

	text string // its statement, as the jobs listing shows it (listedText)
	// command is the name its statement calls when that is one simple
	// command, "" otherwise: jobs -p names such a job by its command's pid
	// (killID).
	command string
	// listed is where it stands among its shell's jobs (jobListing), set
	// as its shell enters it in the table.
	listed jobListing
	// running is closed as the job leaves jobStarting (awaitStart).
	running chan struct{}

	mu    sync.Mutex
	state jobState
	cmds  map[*exec.Cmd]struct{}
	pids  []int // its commands that started, in order
}

// jobPhase is where a job stands: its part runs and started no command
// yet (jobStarting), it started one (jobRunning), its part ended
// (jobEnded), or a wait took its status (jobWaited: one that named it,
// or wait -n), which wait -n takes no more. A job goes through them in
// order; one whose part starts no command goes from jobStarting to
// jobEnded.
type jobPhase int

const (
	jobStarting jobPhase = iota
	jobRunning
	jobEnded
	jobWaited
)

// jobDisposition is what the shell keeps of a job besides running it, as
// disown changes it (job.disown): a job goes from jobOwned to jobNoHangup
// or jobDisowned, and from jobNoHangup to jobDisowned, never back.
type jobDisposition int

const (
	// jobOwned is one of the shell's jobs: jobs lists it, %N names it,
	// wait waits for it, and the shell's hangup reaches it.
	jobOwned jobDisposition = iota
	// jobNoHangup is a job disown -h left the shell's, out of its hangup.
	jobNoHangup
	// jobDisowned is a job disown took out of the shell's jobs: no
	// listing, %N, wait for every job, end notice or hangup. $! (gN) and
	// its pid still name it, as bash's $! does.
	jobDisowned
)

// jobState is a job's phase and what the job learned on its way, which
// the jobs listing and the end notice read (endState). Only the job's
// methods change it, with job.mu held.
type jobState struct {
	phase       jobPhase
	disposition jobDisposition
	// killed is the signal kill ended the job with, in whichever phase
	// before jobEnded it came; 0 when none did.
	killed syscall.Signal
	// died is the signal the job's last command that ended died of, 0
	// for none.
	died   syscall.Signal
	status int // its part's status, once jobEnded
}

// enter moves the job on to phase, closing running as it leaves
// jobStarting. Call it with j.mu held.
func (j *job) enter(phase jobPhase) {
	if phase <= j.state.phase {
		return
	}
	if j.state.phase == jobStarting {
		close(j.running)
	}
	j.state.phase = phase
}

// jobKey is the context value of a job's own part: its *job.
type jobKey struct{}

// jobFrom is the job a handler call in ctx runs in, nil in the foreground.
func jobFrom(ctx context.Context) *job {
	j, _ := ctx.Value(jobKey{}).(*job)
	return j
}

// jobKilled is a job's context's cause once kill ended it with a signal
// that ends a process: the signal its commands get too (StopSignal).
type jobKilled struct{ sig syscall.Signal }

func (k jobKilled) Error() string          { return "killed: " + k.sig.String() }
func (k jobKilled) Signal() syscall.Signal { return k.sig }

// kill sends sig to the job: to the process groups of the commands it
// started that still run (their children too), and, for a signal that ends
// a process, to its own part, whose context ends (it runs no further
// statement, and starts no command). The jobs it started are part of it,
// as a bash job's are of its process group: the table kills them too
// (jobTable.kill).
func (j *job) kill(sig syscall.Signal) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if ends(sig) && j.state.killed == 0 && j.state.phase < jobEnded {
		j.state.killed = sig
		j.cancel(jobKilled{sig})
	}
	j.signalLocked(sig)
}

// signalLocked sends sig to the process group each of the job's running
// commands leads (runJob), and SIGCONT after a signal that ends a
// process, as bash's kill does: a process of the group the terminal
// stopped (a child of the command that read or set the terminal, its
// SIGTTIN or SIGTTOU no longer ignored) acts on the signal only once it
// runs. Call it with j.mu held.
func (j *job) signalLocked(sig syscall.Signal) {
	for cmd := range j.cmds {
		if syscall.Kill(-cmd.Process.Pid, sig) != nil {
			_ = cmd.Process.Signal(sig)
			continue
		}
		if ends(sig) {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT)
		}
	}
}

// within reports whether j is anc or was started inside it, by a job of
// its, at any depth.
func (j *job) within(anc *job) bool {
	for ; j != nil; j = j.parent {
		if j == anc {
			return true
		}
	}
	return false
}

// ends reports whether sig ends a process that takes its default action.
func ends(sig syscall.Signal) bool {
	switch sig {
	case 0, syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU,
		syscall.SIGCONT, syscall.SIGCHLD, syscall.SIGURG, syscall.SIGWINCH:
		return false
	}
	return !ignoredByDefault(sig)
}

// startCommand enters a command the job started, and reports the signal
// kill ended the job with, for a command that started as kill came; 0
// while the job runs. endCommand takes it out once it ended.
func (j *job) startCommand(cmd *exec.Cmd) (killed syscall.Signal) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cmds == nil {
		j.cmds = make(map[*exec.Cmd]struct{})
	}
	j.cmds[cmd] = struct{}{}
	j.pids = append(j.pids, cmd.Process.Pid)
	j.enter(jobRunning)
	return j.state.killed
}

func (j *job) endCommand(cmd *exec.Cmd) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.cmds, cmd)
	j.state.died = 0
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		j.state.died = ws.Signal()
	}
}

// disown takes j out of its shell's jobs (jobDisowned), or, hangupOnly,
// keeps it there and out of the shell's hangup (jobNoHangup).
func (j *job) disown(hangupOnly bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case !hangupOnly:
		j.state.disposition = jobDisowned
	case j.state.disposition == jobOwned:
		j.state.disposition = jobNoHangup
	}
}

// disposition is what the shell keeps of j (jobDisposition).
func (j *job) disposition() jobDisposition {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.disposition
}

// hangsUp reports whether the shell's hangup reaches j: neither it nor a
// job it was started in left it (disown, disown -h).
func (j *job) hangsUp() bool {
	for ; j != nil; j = j.parent {
		if j.disposition() != jobOwned {
			return false
		}
	}
	return true
}

// soleCommand is the pid of the command j is, when its statement is one
// command (j.command) and it started that one; 0 otherwise.
func (j *job) soleCommand() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.command == "" || len(j.pids) != 1 {
		return 0
	}
	return j.pids[0]
}

// partEnded records that the job's part ended with status, and lets go of
// its context.
func (j *job) partEnded(status int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state.status = status
	j.enter(jobEnded)
	j.cancel = nil
}

// end ends the context of the job's own part with cause, while the part
// runs: once it ended, the context is the jobs' it started.
func (j *job) end(cause error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state.phase < jobEnded {
		j.cancel(cause)
	}
}

// awaitStart waits for the job's first command to start, or its part to
// end with none, up to jobStartWait: a job's command starts a moment after
// its &, on the job's own goroutine, and the start notice and jobs -l and
// -p name it by its pid.
func (j *job) awaitStart() {
	select {
	case <-j.running:
	case <-time.After(jobStartWait):
	}
}

// phase is where the job stands.
func (j *job) phase() jobPhase {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.phase
}

// killedBy is the signal kill ended the job with, 0 while it runs.
func (j *job) killedBy() syscall.Signal {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.killed
}

// jobSpawner hands a job, from its own goroutine (runJobPart), to the
// shell that started it (jobStarted), which enters it in the table by the
// interpreter's channel for it: nothing else ties the two. markJobs gives
// each `&` an id of its own, which both calls carry, so a job reaches the
// shell that started it whatever else starts at once (the shells of a
// pipeline). The shell's take waits for its job to arrive or to end
// without its part running (an error before it ran): jobStarted always
// follows its own `&`, so a job that never arrives (one the interpreter
// parsed itself, see inBackgroundJob) has no take waiting for it, and only
// a user's own call of jobStartedCommand with a stale id waits, for the
// newest job's end. A function's `&` keeps one id for every call: its
// jobs queue under it, and calls of it at once (in a pipeline) may swap
// them, which affects only which job $! names there.
type jobSpawner struct {
	mu      sync.Mutex
	arrived map[uint64][]*job // jobs whose part runs, by id, until their shell takes them
	wake    chan struct{}     // closed, and replaced, as a job arrives
}

// jobIDs is the last id markJobs gave a job.
var jobIDs atomic.Uint64

// arrive hands j, the job of id, which just started, to its shell, which
// takes it as its part runs or after it ended. (One whose shell never takes
// it, its line ended between its `&` and its jobStartedCommand, stays.)
func (s *jobSpawner) arrive(id uint64, j *job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.arrived == nil {
		s.arrived = make(map[uint64][]*job)
	}
	s.arrived[id] = append(s.arrived[id], j)
	if s.wake != nil {
		close(s.wake)
		s.wake = nil
	}
}

// takeLocked takes the oldest job that arrived as id out and returns it;
// nil when there is none. Call it with s.mu held.
func (s *jobSpawner) takeLocked(id uint64) *job {
	queue := s.arrived[id]
	if len(queue) == 0 {
		return nil
	}
	if len(queue) == 1 {
		delete(s.arrived, id)
	} else {
		s.arrived[id] = queue[1:]
	}
	return queue[0]
}

// take is the job of id, waiting for it to arrive until done, the
// channel of the job the shell just started, closes: nil when the job
// ended without its part running, or id is no job's markJobs gave.
func (s *jobSpawner) take(id uint64, done <-chan struct{}) *job {
	for {
		s.mu.Lock()
		if j := s.takeLocked(id); j != nil {
			s.mu.Unlock()
			return j
		}
		if id == 0 || id > jobIDs.Load() {
			s.mu.Unlock()
			return nil
		}
		if s.wake == nil {
			s.wake = make(chan struct{})
		}
		wake := s.wake
		s.mu.Unlock()
		select {
		case <-wake:
		case <-done:
			s.mu.Lock()
			j := s.takeLocked(id) // it may have arrived as it ended
			s.mu.Unlock()
			return j
		}
	}
}

// jobTable is the shell's background jobs, each one whose part arrived
// (jobSpawner), by the channel closed when it ends. A job stays in the
// table once it ended (jobEnded), out of the running ones (shellJobs,
// lookup, running), until a wait takes its status (waited): wait finds it
// by the pid of the one command it was (pidJob), as bash keeps its jobs'
// pids and their statuses for wait.
type jobTable struct {
	spawn jobSpawner // the jobs on their way to the shells that started them

	mu      sync.Mutex
	jobs    map[<-chan struct{}]*job
	notices []string // of the jobs that ended, for the interactive shell (JobNotices)
	// ended ends when the shell ends its jobs (SignalJobs), and with it
	// the context of every job's own part (detach).
	ended     context.Context
	end       context.CancelFunc
	endedOnce sync.Once
}

// add enters the job j that ends when done closes, and reports whether it
// runs: false when it ended already, which it enters as it is, or is in
// the table already, which it does not.
func (t *jobTable) add(done <-chan struct{}, j *job) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.jobs[done]; ok || j == nil {
		return false
	}
	if t.jobs == nil {
		t.jobs = make(map[<-chan struct{}]*job)
	}
	t.jobs[done] = j
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// pidJob is the number of the job of shell that is the one command whose
// pid is pid, running or ended, the newest such: jobs -p prints that pid
// for it (killID). 0 when there is none.
func (t *jobTable) pidJob(shell *interp.Runner, pid int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, j := range t.jobs {
		if j.listed.shell == shell && j.soleCommand() == pid {
			n = max(n, j.listed.n)
		}
	}
	return n
}

// lookup is the running job that ends with done, nil when there is none.
func (t *jobTable) lookup(done <-chan struct{}) *job {
	t.mu.Lock()
	defer t.mu.Unlock()
	if j := t.jobs[done]; j != nil && j.phase() < jobEnded {
		return j
	}
	return nil
}

// waited records that a wait took the status of the job that ended with
// done (jobWaited), and takes it out of the table, as bash forgets a job
// it waited for: its pid names no job from then on, and a shell that
// waits for each job it starts keeps none.
func (t *jobTable) waited(done <-chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if j := t.jobs[done]; j != nil {
		j.mu.Lock()
		j.enter(jobWaited)
		j.mu.Unlock()
		delete(t.jobs, done)
	}
}

// running is the channels of the jobs that run now.
func (t *jobTable) running() []<-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]<-chan struct{}, 0, len(t.jobs))
	for done, j := range t.jobs {
		if j.phase() < jobEnded {
			out = append(out, done)
		}
	}
	return out
}

// hangup sends sig to the process group of every command of a job that
// still runs and that the shell's hangup reaches (hangsUp).
func (t *jobTable) hangup(sig syscall.Signal) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, j := range t.jobs {
		if !j.hangsUp() {
			continue
		}
		j.mu.Lock()
		j.signalLocked(sig)
		j.mu.Unlock()
	}
}

// notDisowned is jobs, a shell's jobs by their channels, but the ones
// disown took out of its jobs, which a bare wait does not wait for.
func (t *jobTable) notDisowned(jobs []<-chan struct{}) []<-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(jobs), func(done <-chan struct{}) bool {
		j := t.jobs[done]
		return j != nil && j.disposition() == jobDisowned
	})
}

// disowned reports whether the running job n of shell is one disown took
// out of its jobs.
func (t *jobTable) disowned(shell *interp.Runner, n int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, j := range t.jobs {
		if j.listed == (jobListing{n: n, shell: shell}) {
			return j.disposition() == jobDisowned
		}
	}
	return false
}

// kill kills (job.kill) the job j and every running job started inside it.
func (t *jobTable) kill(j *job, sig syscall.Signal) {
	t.mu.Lock()
	var inside []*job
	for _, other := range t.jobs {
		if other != j && other.within(j) {
			inside = append(inside, other)
		}
	}
	t.mu.Unlock()
	j.kill(sig)
	for _, other := range inside {
		other.kill(sig)
	}
}

// endedContext is the context that ends when the shell ends its jobs.
func (t *jobTable) endedContext() context.Context {
	t.endedOnce.Do(func() { t.ended, t.end = context.WithCancel(context.Background()) })
	return t.ended
}

// endAll ends the context of every job's own part, now and from now on.
func (t *jobTable) endAll() {
	t.endedContext()
	t.end()
}

// ErrLineInterrupted is the cause a caller ends a line's context with on
// Ctrl+C: the line's own work stops, and its background jobs run on, as a
// bash job that ignores Ctrl+C does. A line context that ends for any
// other cause (the shell ending) ends its jobs' own parts too.
var ErrLineInterrupted = errors.New("line interrupted")

// detach is the job whose own part runs on ctx's values (its line's, its
// lineRun), on a context of its own (own): not ended with its line by Ctrl+C
// (ErrLineInterrupted). It ends when the line's context ends for another
// cause, when kill ends the job, or when the shell ends its jobs
// (endAll). A job a job starts follows the same line. release, once the
// job's part ended, stops the following and leaves the context as it is: a
// job the part started runs on it until it reaches a context of its own.
func (t *jobTable) detach(ctx context.Context, text string, substOut []*lockedWriter) (j *job, own context.Context, release func()) {
	line := ctx
	if parent := jobFrom(ctx); parent != nil {
		line = parent.line
	} else if lr := lineRunFrom(ctx); lr != nil && lr.jobsFollow != nil {
		line = lr.jobsFollow
	}
	j = &job{parent: jobFrom(ctx), text: text, substOut: substOut, line: line, running: make(chan struct{})}
	own, j.cancel = context.WithCancelCause(context.WithValue(context.WithoutCancel(ctx), jobKey{}, j))
	stopShell := context.AfterFunc(t.endedContext(), func() { j.end(nil) })
	stopLine := context.AfterFunc(line, func() {
		if !errors.Is(context.Cause(line), ErrLineInterrupted) {
			j.end(context.Cause(line))
		}
	})
	return j, own, func() {
		stopShell()
		stopLine()
	}
}

// jobStarted enters the calling shell's newest job, the job of the id
// args name, in the table, for the line it belongs to (the line ctx
// carries: the one that started it, or started the job that did), and
// returns the call that leaves $? at 0, as the `&` before it did. It runs
// on the calling shell's goroutine, right after its `&`
// (jobStartedCommand), and waits for the job's part to arrive
// (jobSpawner), or for the job to end.
func (e *Executor) jobStarted(ctx context.Context, args []string) []string {
	jobs, ok := runnerJobs(e.callerRunner(ctx))
	if !ok || jobs.count() == 0 || len(args) != 2 {
		return []string{":"}
	}
	id, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		return []string{":"}
	}
	done := jobs.done(jobs.count())
	j := e.jobs.spawn.take(id, done)
	if j == nil {
		return []string{":"}
	}
	j.listed = jobListing{n: jobs.count(), shell: e.callerRunner(ctx)}
	notify := e.notifiesJobs(ctx, j)
	if notify {
		e.noteJobStart(interp.HandlerCtx(ctx).Stderr, j)
	}
	if !e.jobs.add(done, j) { // it ended already
		if notify {
			e.jobs.noteEnd(j)
		}
		return []string{":"}
	}
	lr := lineRunFrom(ctx)
	if lr != nil {
		lr.jobs.start(done)
	}
	go func() {
		<-done
		if notify {
			e.jobs.noteEnd(j)
		}
		if lr != nil {
			lr.jobs.end(done)
		}
	}()
	return []string{":"}
}

// lineJobs is the background jobs of one line: the ones it started and the
// ones they started, while any runs. The line's caller learns from it when
// the last one ended (Result.JobsDone).
type lineJobs struct {
	mu      sync.Mutex
	state   lineJobsState
	running map[<-chan struct{}]struct{} // by the channel closed when the job ends
	done    chan struct{}                // lineJobsOutlive: closed as the last job ends
}

// lineJobsState is where a line stands with its jobs. The states go in
// this order; a line that returns with none running skips the middle one.
type lineJobsState int

const (
	lineJobsLineRuns lineJobsState = iota // the line runs: its jobs come and go
	lineJobsOutlive                       // it returned with jobs running: done closes as the last ends
	lineJobsAllEnded                      // it returned, and none of its jobs runs
)

func (j *lineJobs) start(job <-chan struct{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running == nil {
		j.running = make(map[<-chan struct{}]struct{})
	}
	j.running[job] = struct{}{}
}

func (j *lineJobs) end(job <-chan struct{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.running, job)
	if len(j.running) == 0 && j.state == lineJobsOutlive {
		j.state = lineJobsAllEnded
		close(j.done)
	}
}

// lineReturned is what Result.JobsDone says of the line as it returns:
// nil when none of its jobs runs any more, else a channel closed when the
// last one ends.
func (j *lineJobs) lineReturned() <-chan struct{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	for job := range j.running {
		select {
		case <-job: // ended; its end is on its way
			delete(j.running, job)
		default:
		}
	}
	if len(j.running) == 0 {
		j.state = lineJobsAllEnded
		return nil
	}
	j.state, j.done = lineJobsOutlive, make(chan struct{})
	return j.done
}

// SignalJobs sends sig to every command of a background job that still
// runs, but a disowned job's (jobDisposition). The interactive shell calls
// it as a fatal signal ends it, before its EXIT trap, as bash hands its
// jobs the SIGHUP that ends it. (A script leaves its jobs alone, as bash
// does.)
func (e *Executor) SignalJobs(sig syscall.Signal) {
	e.jobs.endAll()
	e.jobs.hangup(sig)
}

// jobRunCommand runs a job's own statements, as hash parsed them, as the
// job's part (runJobPart). markBackgroundJobs makes each `cmd &` run
// `{ __hash_job_run ID 'cmd'; } &`.
const jobRunCommand = "__hash_job_run"

// runJobPart runs a job's statements (args[2], printed by
// markBackgroundJobs) on the job's own runner, on a context of the job's
// own (detach), and returns the call that carries their status: 128+signal
// when kill ended it. They are parsed again here, with the shell's
// dialect, and their own jobs marked. The job arrives at the shell that
// started it first, by its id, args[1] (jobSpawner). A call with no id
// runs nothing.
func (e *Executor) runJobPart(ctx context.Context, args []string) []string {
	if len(args) != 3 {
		return statusCall(2)
	}
	id, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		return statusCall(2)
	}
	prog, err := shparse.Parse(e.newParser(), args[2], "")
	if err != nil {
		prog = nil
	}
	j, own, release := e.jobs.detach(ctx, listedText(prog, args[2]), heldSubstitutions(interp.HandlerCtx(ctx), prog))
	defer release()
	j.command = simpleCommandName(prog)
	e.jobs.spawn.arrive(id, j)
	if prog == nil {
		status := syntaxErrorCall(ctx, "job", err)
		j.partEnded(callStatus(status))
		return status
	}
	e.prepare(prog)
	status := runNested(own, e.callerRunner(ctx), prog, "job")
	if sig := j.killedBy(); sig != 0 {
		status = statusCall(128 + int(sig))
	}
	j.partEnded(callStatus(status))
	return status
}

// substBegin makes the writes into a command substitution that starts jobs
// reach it one at a time: the jobs it starts inherit its stdout, and write
// into it as it does (endSubstitutionsWithTheirJobs). It returns the call
// that keeps $?.
func substBegin(ctx context.Context) []string {
	hc := interp.HandlerCtx(ctx)
	if r := handlerRunner(hc); r != nil {
		out := &lockedWriter{w: hc.Stdout}
		_ = interp.StdIO(hc.Stdin, out, hc.Stderr)(r)
	}
	return statusCall(hc.LastExitStatus)
}

// lockedWriter writes to w one write at a time.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// substEnd waits, at the end of a command substitution, for the jobs it
// started that hold its output open (endSubstitutionsWithTheirJobs,
// job.substOut), or for its line to end, and returns the call that keeps
// the substitution's status.
func (e *Executor) substEnd(ctx context.Context) []string {
	hc := interp.HandlerCtx(ctx)
	out, _ := hc.Stdout.(*lockedWriter)
	jobs, _ := runnerJobs(handlerRunner(hc))
	for _, done := range jobs.all() {
		if j := e.jobs.lookup(done); j != nil && out != nil && !slices.Contains(j.substOut, out) {
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			return statusCall(128 + int(StopSignal(ctx)))
		}
	}
	return statusCall(hc.LastExitStatus)
}

// simpleCommandName is the name prog, a job's statement, calls when it is
// one simple command with a literal name; "" otherwise (or prog is nil).
func simpleCommandName(prog *syntax.File) string {
	if prog == nil || len(prog.Stmts) != 1 {
		return ""
	}
	call, ok := prog.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return ""
	}
	name, _ := literalWord(call.Args[0])
	return name
}

// listedText is how the jobs listing shows a job's statement prog,
// printed from text: on one line, as bash lists `{ a; b; } &`; text itself
// when prog did not parse.
func listedText(prog *syntax.File, text string) string {
	var b strings.Builder
	if prog == nil || syntax.NewPrinter(syntax.SingleLine(true)).Print(&b, prog) != nil {
		return text
	}
	return strings.TrimSpace(b.String())
}

// heldSubstitutions are the command substitutions' writers a job holds
// open as it runs, the job's statement being prog's (nil when it did not
// parse) and hc the call that starts it: the job's stdout and stderr, when
// each is a substitution's writer (substBegin) and its statement's
// redirections leave it open (heldStreams).
func heldSubstitutions(hc interp.HandlerContext, prog *syntax.File) []*lockedWriter {
	stdout, stderr := true, true
	if prog != nil && len(prog.Stmts) == 1 {
		stdout, stderr = heldStreams(prog.Stmts[0].Redirs)
	}
	var held []*lockedWriter
	for _, s := range []struct {
		held bool
		w    io.Writer
	}{{stdout, hc.Stdout}, {stderr, hc.Stderr}} {
		if lw, ok := s.w.(*lockedWriter); ok && s.held {
			held = append(held, lw)
		}
	}
	return held
}

// heldStreams reports whether a statement with redirs keeps its stdout,
// and its stderr, open on one of its descriptors once the redirections
// applied: `>log 2>&1` keeps neither, `2>&1 >log` keeps the stdout on its
// descriptor 2. A redirection it cannot follow (a descriptor an expansion
// names) keeps both.
func heldStreams(redirs []*syntax.Redirect) (stdout, stderr bool) {
	fds := map[int]heldStream{1: heldStdout, 2: heldStderr}
	for _, rd := range redirs {
		if !followRedirect(fds, rd) {
			return true, true
		}
	}
	for _, s := range fds {
		stdout = stdout || s == heldStdout
		stderr = stderr || s == heldStderr
	}
	return stdout, stderr
}

// heldStream is the stream a statement inherited that one of its
// descriptors leads to (heldStreams); a descriptor that leads elsewhere is
// none.
type heldStream int

const (
	heldStdout heldStream = iota + 1
	heldStderr
)

// followRedirect applies rd to fds, the streams each descriptor leads to;
// false when it cannot tell where rd leads.
func followRedirect(fds map[int]heldStream, rd *syntax.Redirect) bool {
	n, ok := redirectFd(rd)
	if !ok {
		return false // {varname}>
	}
	switch rd.Op {
	case syntax.RdrAll, syntax.AppAll, syntax.RdrAllClob, syntax.AppAllClob:
		delete(fds, 1)
		delete(fds, 2)
		return true
	case syntax.DplIn, syntax.DplOut:
	default:
		delete(fds, n)
		return true
	}
	word, literal := literalWord(rd.Word)
	if !literal {
		return false
	}
	m, err := strconv.Atoi(word)
	switch {
	case word == "-":
		delete(fds, n)
	case err != nil && rd.N == nil && rd.Op == syntax.DplOut: // >&file is &>file
		delete(fds, 1)
		delete(fds, 2)
	case err != nil:
		return false
	default:
		if s, open := fds[m]; open {
			fds[n] = s
		} else {
			delete(fds, n)
		}
	}
	return true
}

// redirectFd is the descriptor rd redirects: the one it names, else 0 for
// an input and 1 for an output; ok is false for one an expansion names.
func redirectFd(rd *syntax.Redirect) (n int, ok bool) {
	if rd.N != nil {
		n, err := strconv.Atoi(rd.N.Value)
		return n, err == nil
	}
	switch rd.Op {
	case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return 0, true
	}
	return 1, true
}

// LetJobsStart gives the background jobs up to grace to end before the shell
// exits, so that a job started just before the exit starts its command:
// bash forks a job at its &, where hash's job is a goroutine that starts
// its command a moment later, and an exit at once would never start it
// (`hash -c 'server &'`). It does not wait longer: a job's command keeps
// running after hash exits, as bash's does, and a job hash runs itself
// (a function, a builtin, a pipeline it copies) is cut off.
func (e *Executor) LetJobsStart(grace time.Duration) {
	timeout := time.After(grace)
	for _, done := range e.jobs.running() {
		select {
		case <-done:
		case <-timeout:
			return
		}
	}
}

// markBackgroundJobs rewrites each `cmd &` in prog to run
// `{ __hash_job_run 'cmd'; } &`, its redirections in the text too: the
// job's statements run, as text, on a context of the job's own
// (runJobPart), which is how hash tells a job from the foreground (mvdan/sh
// gives handlers no other way, see inBackgroundJob), and which kill ends.
// It follows the `&` with a call of jobStartedCommand, which enters the job
// in the shell's table (jobStarted).
func markBackgroundJobs(prog *syntax.File) {
	syntax.Walk(prog, func(node syntax.Node) bool {
		for _, list := range stmtLists(node) {
			*list = markJobs(*list)
		}
		return true
	})
}

// markJobs is list with each background statement made a job, followed by
// a call of jobStartedCommand, both with the job's id (jobIDs). A
// statement the printer cannot print (it prints whatever the parser makes)
// runs as it is, as a job the interpreter parsed itself would (see
// inBackgroundJob).
func markJobs(list []*syntax.Stmt) []*syntax.Stmt {
	out := list[:0:0]
	for _, st := range list {
		out = append(out, st)
		if (!st.Background && !st.Disown) || st.Cmd == nil {
			continue
		}
		var text strings.Builder
		own := &syntax.Stmt{Cmd: st.Cmd, Redirs: st.Redirs, Negated: st.Negated}
		if err := syntax.NewPrinter().Print(&text, own); err != nil {
			continue
		}
		id := strconv.FormatUint(jobIDs.Add(1), 10)
		st.Cmd = &syntax.Block{Stmts: []*syntax.Stmt{{Cmd: &syntax.CallExpr{Args: []*syntax.Word{
			litWord(jobRunCommand), litWord(id), quotedWord(text.String()),
		}}}}}
		st.Redirs, st.Negated = nil, false
		out = append(out, &syntax.Stmt{Cmd: &syntax.CallExpr{Args: []*syntax.Word{litWord(jobStartedCommand), litWord(id)}}})
	}
	return out
}

// litWord is the word of a plain literal.
func litWord(s string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: s}}}
}

// quotedWord is a word that expands to s as it is: single-quoted, with
// each ' in it escaped outside the quotes.
func quotedWord(s string) *syntax.Word {
	w := &syntax.Word{}
	for i, part := range strings.Split(s, "'") {
		if i > 0 {
			w.Parts = append(w.Parts, &syntax.Lit{Value: `\'`})
		}
		w.Parts = append(w.Parts, &syntax.SglQuoted{Value: part})
	}
	return w
}

// stmtLists is the statement lists node holds directly.
func stmtLists(node syntax.Node) []*[]*syntax.Stmt {
	switch n := node.(type) {
	case *syntax.File:
		return []*[]*syntax.Stmt{&n.Stmts}
	case *syntax.Block:
		return []*[]*syntax.Stmt{&n.Stmts}
	case *syntax.Subshell:
		return []*[]*syntax.Stmt{&n.Stmts}
	case *syntax.CmdSubst:
		return []*[]*syntax.Stmt{&n.Stmts}
	case *syntax.ProcSubst:
		return []*[]*syntax.Stmt{&n.Stmts}
	case *syntax.IfClause:
		return []*[]*syntax.Stmt{&n.Cond, &n.Then}
	case *syntax.WhileClause:
		return []*[]*syntax.Stmt{&n.Cond, &n.Do}
	case *syntax.ForClause:
		return []*[]*syntax.Stmt{&n.Do}
	case *syntax.CaseItem:
		return []*[]*syntax.Stmt{&n.Stmts}
	}
	return nil
}
