package executor

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"mvdan.cc/sh/v3/interp"
)

// jobListing is where a job stands among the jobs of the shell that
// started it: the jobs builtin lists a shell's own jobs, by number, and the
// interactive shell names its jobs so as they start and end.
type jobListing struct {
	n     int            // its number in its shell: gN, %N
	shell *interp.Runner // the shell (or subshell) that started it
}

// jobStartWait is how long the start notice waits for a job's first
// command, for the pid it names.
const jobStartWait = 20 * time.Millisecond

// notifiesJobs reports whether the shell tells of j as it starts and ends,
// as bash -i does: a job the interactive shell itself started on a line the
// user runs, not one of a subshell, a job or a startup file.
func (e *Executor) notifiesJobs(ctx context.Context, j *job) bool {
	lr := lineRunFrom(ctx)
	return e.terminal == terminalInteractive && lr != nil && lr.kind == lineUser &&
		j.parent == nil && j.listed.shell == e.runner
}

// noteJobStart writes the notice of j's start to w, as bash's `[1] 12345`:
// the pid of its first command, or its gN while it has started none
// (shell code hash runs, a command still on its way after jobStartWait).
func (e *Executor) noteJobStart(w io.Writer, j *job) {
	j.awaitStart()
	j.mu.Lock()
	id := "g" + strconv.Itoa(j.listed.n)
	if len(j.pids) > 0 {
		id = strconv.Itoa(j.pids[0])
	}
	j.mu.Unlock()
	fmt.Fprintf(w, "[%d] %s\n", j.listed.n, id)
}

// noteEnd queues the notice of j's end, which the interactive shell prints
// before its next prompt (JobNotices): `[1]+  Done   sleep 1 &`.
func (t *jobTable) noteEnd(j *job) {
	if j.disposition() == jobDisowned {
		return
	}
	mark := t.mark(j)
	j.mu.Lock()
	state := j.endState()
	j.mu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.notices = append(t.notices, fmt.Sprintf("[%d]%c  %-24s%s &", j.listed.n, mark, state, j.text))
}

// JobNotices returns the notices of the jobs that ended since the last
// call, for the interactive shell to print before its prompt, as bash does.
func (e *Executor) JobNotices() []string {
	e.jobs.mu.Lock()
	defer e.jobs.mu.Unlock()
	notices := e.jobs.notices
	e.jobs.notices = nil
	return notices
}

// endState is how a job that ended is listed, in bash's words: Done, Exit N,
// or the signal that ended it (Terminated). Call it with j.mu held.
func (j *job) endState() string {
	st := j.state
	switch {
	case st.killed != 0:
		return signalWords(st.killed)
	case st.status == 0:
		return "Done"
	case st.died != 0 && st.status == 128+int(st.died):
		return signalWords(st.died)
	}
	return "Exit " + strconv.Itoa(st.status)
}

// signalWords is the description of sig, capitalized, as bash words a
// job's death: Terminated, Killed, Hangup.
func signalWords(sig syscall.Signal) string {
	s := sig.String()
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// shellJobList is the running jobs of a shell, by number: the ones jobs
// lists, the newest marked + (current), the one before it - (previous),
// which %%, %+ and %- name (jobNumber); %- names the only one too.
type shellJobList []*job

// current is the newest running job, nil when none runs.
func (l shellJobList) current() *job {
	if len(l) == 0 {
		return nil
	}
	return l[len(l)-1]
}

// previous is the running job before the current one; the current one
// when it is the only one, as bash's %- names a single job; nil when none
// runs.
func (l shellJobList) previous() *job {
	if len(l) < 2 {
		return l.current()
	}
	return l[len(l)-2]
}

// shellJobs is the running jobs shell started, by number: not one whose
// part ended (jobEnded), which the table keeps, nor one disown took out
// (jobDisowned).
func (t *jobTable) shellJobs(shell *interp.Runner) shellJobList {
	return t.shellJobsWith(shell, nil)
}

// shellJobsWith is shellJobs with also, a job of shell's, whether or not
// it ended or a wait took it out of the table (waited).
func (t *jobTable) shellJobsWith(shell *interp.Runner, also *job) shellJobList {
	t.mu.Lock()
	defer t.mu.Unlock()
	var jobs shellJobList
	if also != nil {
		jobs = append(jobs, also)
	}
	for _, j := range t.jobs {
		if j != also && j.listed.shell == shell && j.phase() < jobEnded && j.disposition() != jobDisowned {
			jobs = append(jobs, j)
		}
	}
	slices.SortFunc(jobs, func(a, b *job) int { return a.listed.n - b.listed.n })
	return jobs
}

// unwaited is the jobs of shell wait -n takes, by number: the running
// ones (shellJobs) and the ones that ended and that no wait took
// (jobWaited).
func (t *jobTable) unwaited(shell *interp.Runner) []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	var ns []int
	for _, j := range t.jobs {
		if j.listed.shell == shell && j.phase() < jobWaited && j.disposition() != jobDisowned {
			ns = append(ns, j.listed.n)
		}
	}
	slices.Sort(ns)
	return ns
}

// mark is j's mark among its shell's running jobs and itself, as bash's: +
// for the newest, - for the one before it.
func (t *jobTable) mark(j *job) byte {
	jobs := t.shellJobsWith(j.listed.shell, j)
	return markAt(slices.Index(jobs, j), len(jobs))
}

// jobsCall lists the calling shell's running jobs, as bash's jobs builtin:
// `[1]+  Running   sleep 100 &`; -l adds the pids of each job's commands,
// -p lists only those, -r lists the running ones (every one) and -s the
// stopped ones (none: hash stops no job). Its operands name the jobs to
// list (jobNumber); one that names none fails the call with 1. A subshell
// that started none lists the shell's, as in bash (`jobs | wc -l`); a
// background job only its own. It returns the call that carries its
// status.
func (e *Executor) jobsCall(ctx context.Context, args []string) []string {
	hc := interp.HandlerCtx(ctx)
	opts, ops, bad := parseJobsOptions(args[1:])
	if bad != 0 {
		fmt.Fprintf(hc.Stderr, "hash: jobs: -%c: invalid option\njobs: usage: jobs [-lprs] [jobspec ...]\n", bad)
		return statusCall(2)
	}
	shell := e.callerRunner(ctx)
	jobs := e.jobs.shellJobs(shell)
	if len(jobs) == 0 && shell != e.runner && !inBackgroundJob(ctx) {
		shell = e.runner
		jobs = e.jobs.shellJobs(shell)
	}
	listed, status := jobs, 0
	if len(ops) > 0 {
		listed = nil
		count, _ := runnerJobs(shell)
		for _, op := range ops {
			n, found := e.jobs.jobNumber(shell, op, count.count())
			i := slices.IndexFunc(jobs, func(j *job) bool { return j.listed.n == n })
			if !found || i < 0 {
				fmt.Fprintf(hc.Stderr, "hash: jobs: %s: no such job\n", op)
				status = 1
				continue
			}
			listed = append(listed, jobs[i])
		}
	}
	if opts.stopped {
		listed = nil
	}
	for _, j := range listed {
		if opts.long || opts.pids {
			j.awaitStart()
		}
		_, isFunc := e.callerRunner(ctx).Funcs[j.command]
		listJob(hc.Stdout, j, markAt(slices.Index(jobs, j), len(jobs)), opts.long, opts.pids, isFunc)
	}
	return statusCall(status)
}

// jobsOptions are the options of a jobs call.
type jobsOptions struct{ long, pids, stopped bool }

// parseJobsOptions reads a jobs call's arguments: its options, alone or
// together (-lp), and its operands; bad is an option it does not take.
func parseJobsOptions(args []string) (opts jobsOptions, ops []string, bad rune) {
	for len(args) > 0 && len(args[0]) > 1 && args[0][0] == '-' {
		opt := args[0]
		args = args[1:]
		if opt == "--" {
			break
		}
		for _, c := range opt[1:] {
			switch c {
			case 'l':
				opts.long = true
			case 'p':
				opts.pids = true
			case 's':
				opts.stopped = true
			case 'r':
			default:
				return opts, nil, c
			}
		}
	}
	return opts, args, 0
}

// markAt is the mark of the ith of n running jobs, as bash's: + for the
// newest, - for the one before it.
func markAt(i, n int) byte {
	switch i {
	case n - 1:
		return '+'
	case n - 2:
		return '-'
	}
	return ' '
}

// listJob writes j's line of the jobs listing to w, its mark mark: with the
// pids of its commands that run for long; for pids only the id that ends
// it (killID), its command a function when isFunc.
func listJob(w io.Writer, j *job, mark byte, long, pids, isFunc bool) {
	running := j.runningPids()
	switch {
	case pids:
		fmt.Fprintln(w, j.killID(running, isFunc))
	case long:
		fmt.Fprintf(w, "[%d]%c %s %-24s%s &\n", j.listed.n, mark, strings.Join(running, " "), "Running", j.text)
	default:
		fmt.Fprintf(w, "[%d]%c  %-24s%s &\n", j.listed.n, mark, "Running", j.text)
	}
}

// runningPids is the pids of j's commands that run, in the order they
// started.
func (j *job) runningPids() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	running := make([]string, 0, len(j.cmds))
	for _, pid := range j.pids {
		for cmd := range j.cmds {
			if cmd.Process.Pid == pid {
				running = append(running, strconv.Itoa(pid))
			}
		}
	}
	return running
}

// killID is how jobs -p names j, running the commands of pids running, for
// kill to end it: the pid of its command when its statement is one
// command (j.command, no function: isFunc false) and that command runs;
// else its gN, which kill takes for the whole job, where one pid would end
// only the command that runs then (of a { ...; } block, a pipeline, a
// function) or none (shell code).
func (j *job) killID(running []string, isFunc bool) string {
	if j.command != "" && !isFunc && len(running) == 1 {
		return running[0]
	}
	return "g" + strconv.Itoa(j.listed.n)
}

// callStatus is the status a call of statusCall carries.
func callStatus(call []string) int {
	if len(call) == 2 && call[0] == statusCommand {
		code, _ := strconv.Atoi(call[1])
		return code
	}
	return 0
}

// disownCall is bash's disown [-h] [-ar] [jobspec ... | pid ...]: it takes
// the jobs it names (the current one when it names none, every running
// one for -a or -r) out of the calling shell's jobs, or with -h out of its
// hangup only (job.disown). It returns the call that carries its status:
// 1 when an operand names no running job, 2 for a bad option.
func (e *Executor) disownCall(ctx context.Context, args []string) []string {
	stderr := interp.HandlerCtx(ctx).Stderr
	hangupOnly, all, ops, bad := parseDisown(args[1:])
	if bad != 0 {
		fmt.Fprintf(stderr, "hash: disown: -%c: invalid option\ndisown: usage: disown [-h] [-ar] [jobspec ... | pid ...]\n", bad)
		return statusCall(2)
	}
	caller := e.callerRunner(ctx)
	running := e.jobs.shellJobs(caller)
	switch {
	case all:
		ops = nil
	case len(ops) == 0 && running.current() == nil:
		fmt.Fprintln(stderr, "hash: disown: current: no such job")
		return statusCall(1)
	case len(ops) == 0:
		running = shellJobList{running.current()}
	default:
		running = nil
	}
	jobs, _ := runnerJobs(caller)
	status := 0
	for _, op := range ops {
		var j *job
		if ns, bad := e.jobs.named(caller, []string{op}, jobs.count()); bad == "" {
			j = e.jobs.lookup(jobs.done(ns[0]))
		}
		if j == nil {
			fmt.Fprintf(stderr, "hash: disown: %s: no such job\n", op)
			status = 1
			continue
		}
		running = append(running, j)
	}
	for _, j := range running {
		j.disown(hangupOnly)
	}
	return statusCall(status)
}

// parseDisown reads disown's arguments: -h, -a or -r, and the operands;
// bad is an option it does not take.
func parseDisown(args []string) (hangupOnly, all bool, ops []string, bad rune) {
	ops = args
	for len(ops) > 0 && len(ops[0]) > 1 && ops[0][0] == '-' {
		opt := ops[0]
		ops = ops[1:]
		if opt == "--" {
			break
		}
		for _, c := range opt[1:] {
			switch c {
			case 'h':
				hangupOnly = true
			case 'a', 'r':
				all = true
			default:
				return false, false, nil, c
			}
		}
	}
	return hangupOnly, all, ops, 0
}
