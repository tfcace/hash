package executor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/interp"
)

// handleKillCall runs kill as the system's kill command: mvdan/sh lists kill
// as a builtin but has none ("unsupported builtin"). The call names the
// command by its path, so the runner skips the builtin. A kill function
// keeps the call, and so does a system without kill. A call that names a
// job (%N, %%, $!) is hash's own (killJobs).
func (e *Executor) handleKillCall(ctx context.Context, args []string) []string {
	if _, isFunc := e.callerRunner(ctx).Funcs["kill"]; isFunc {
		return args
	}
	return e.killCommand(ctx, args)
}

// killCommand is a kill call as a call of the system's kill command by its
// path, or the call as it is on a system without kill; a call that names a
// job hash runs itself (killJobs).
func (e *Executor) killCommand(ctx context.Context, args []string) []string {
	if call, ok := e.killJobs(ctx, args); ok {
		return call
	}
	hc := interp.HandlerCtx(ctx)
	path, err := interp.LookPathDir(hc.Dir, hc.Env, "kill")
	if err != nil {
		return args
	}
	return append([]string{path}, args[1:]...)
}

// killJobs runs a kill call that names a job, by its number (%N, gN, $!)
// or as the last (%%, %+) or the one before it (%-), as bash takes them: a
// job is a goroutine of hash's with no pid of its own, so the system's kill
// cannot reach it. The job's own part ends and its commands get the signal
// (job.kill); a pid the call names too gets it as from kill. ok is false
// for a call that names no job, which the system's kill runs. %N is gN:
// the calling shell's Nth job, where bash numbers the jobs that still run.
func (e *Executor) killJobs(ctx context.Context, args []string) (call []string, ok bool) {
	sig, operands, ok := parseKill(args[1:])
	if !ok || !namesJob(operands) {
		return nil, false
	}
	stderr := interp.HandlerCtx(ctx).Stderr
	caller := e.callerRunner(ctx)
	jobs, _ := runnerJobs(caller)
	status := 0
	for _, op := range operands {
		if !isJobSpec(op) {
			pid, err := strconv.Atoi(op)
			if err == nil {
				err = syscall.Kill(pid, sig)
			}
			if err != nil {
				fmt.Fprintf(stderr, "hash: kill: %s: %v\n", op, err)
				status = 1
			}
			continue
		}
		n, found := e.jobs.jobNumber(caller, op, jobs.count())
		var j *job
		if found {
			j = e.jobs.lookup(jobs.done(n))
		}
		if j == nil {
			fmt.Fprintf(stderr, "hash: kill: %s: no such job\n", op)
			status = 1
			continue
		}
		e.jobs.kill(j, sig)
	}
	return statusCall(status), true
}

// parseKill reads a kill call's arguments: its signal (-s SIG, -n NUM,
// -SIG, -NUM; SIGTERM when none) and its operands. ok is false for a call
// it does not take (-l, a signal it does not know), left to the system's
// kill.
func parseKill(args []string) (sig syscall.Signal, operands []string, ok bool) {
	sig = syscall.SIGTERM
	if len(args) > 0 {
		switch a := args[0]; {
		case a == "-s" || a == "-n":
			if len(args) < 2 {
				return 0, nil, false
			}
			sig, ok = signalNamed(args[1])
			args = args[2:]
		case a == "--":
			ok = true
		case strings.HasPrefix(a, "-") && len(a) > 1 && a != "-l" && a != "-L":
			sig, ok = signalNamed(a[1:])
			args = args[1:]
		default:
			ok = true
		}
		if !ok {
			return 0, nil, false
		}
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	return sig, args, true
}

// signalNamed is the signal a kill call names: by number, or by name with
// or without SIG, in any case.
func signalNamed(name string) (syscall.Signal, bool) {
	if n, err := strconv.Atoi(name); err == nil {
		return syscall.Signal(n), n >= 0 && n < 64
	}
	name = strings.ToUpper(name)
	if !strings.HasPrefix(name, "SIG") {
		name = "SIG" + name
	}
	sig := unix.SignalNum(name)
	return sig, sig != 0
}

// isJobSpec reports whether a kill or wait operand names a job: %... or
// the interpreter's gN.
func isJobSpec(op string) bool {
	if strings.HasPrefix(op, "%") {
		return true
	}
	n, err := strconv.Atoi(strings.TrimPrefix(op, "g"))
	return strings.HasPrefix(op, "g") && err == nil && n > 0
}

func namesJob(operands []string) bool {
	for _, op := range operands {
		if isJobSpec(op) {
			return true
		}
	}
	return false
}

// jobNumber is the number (1-based) of the job op names among shell's
// count jobs: %N (not a disowned one) or gN by its number, %%, %+ and %
// the current of the shell's running jobs, %- the previous
// (shellJobList), as jobs marks them. found is false when it names none.
func (t *jobTable) jobNumber(shell *interp.Runner, op string, count int) (n int, found bool) {
	var named *job
	switch op {
	case "%%", "%+", "%":
		named = t.shellJobs(shell).current()
	case "%-":
		named = t.shellJobs(shell).previous()
	default:
		var err error
		if n, err = strconv.Atoi(op[1:]); err != nil { // %N or gN
			return 0, false
		}
		return n, n >= 1 && n <= count && (op[0] != '%' || !t.disowned(shell, n))
	}
	if named == nil {
		return 0, false
	}
	return named.listed.n, named.listed.n <= count
}
