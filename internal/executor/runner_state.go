package executor

import (
	"reflect"
	"sync"

	"mvdan.cc/sh/v3/interp"
)

// mvdan.cc/sh keeps some state hash must read or set and has no API for.
// This file is the one place hash reaches into it, each reach with a
// contract test in runner_state_test.go that drives the behavior hash
// relies on, so a dependency update that moves or retypes a field fails a
// test instead of quietly turning a feature off:
//
//	Runner.filename        read   $0 inside and after a nested program (runNested)
//	Runner.callbackErr     read   the ERR trap, for hash's trap listing
//	Runner.lastExit        both   $? without running a command (SetLastStatus); $? before the EXIT trap
//	HandlerContext.runner  read   the shell making a handler call (callerRunner)
//	Runner.bgProcs[].done  read   when each background job ended (wait, the job table)
//	Runner.opts            read   whether errexit is on, for how a line ended (lineEnd)
//	Runner.inFunc/inSource read   whether a return has anything to return from (returnCall)
//
// Every accessor degrades to "absent" when its field is gone.

// runnerScriptName is the name of the script r runs, its $0; ok is false
// when the field is gone.
func runnerScriptName(r *interp.Runner) (name string, ok bool) {
	f := runnerField(r, reflect.String, "filename")
	if !f.IsValid() {
		return "", false
	}
	return f.String(), true
}

// runnerErrTrap is r's ERR trap action, "" for none.
func runnerErrTrap(r *interp.Runner) string {
	if r == nil {
		return ""
	}
	if f := runnerField(r, reflect.String, "callbackErr"); f.IsValid() {
		return f.String()
	}
	return ""
}

// setRunnerLastStatus makes code r's last status, $?, as if a command had
// just exited with it. The whole status goes: a failed command's error kept
// beside a new code would be what a bare exit exits with.
func setRunnerLastStatus(r *interp.Runner, code int) {
	if f := runnerField(r, reflect.Struct, "lastExit"); f.IsValid() {
		f.SetZero()
	}
	if f := runnerField(r, reflect.Uint8, "lastExit", "code"); f.IsValid() {
		f.SetUint(uint64(code & 0xff)) //nolint:gosec // G115: masked to a status byte
	}
}

// runnerLastStatus is r's last status, $?; ok is false when the field is
// gone.
func runnerLastStatus(r *interp.Runner) (code int, ok bool) {
	f := runnerField(r, reflect.Uint8, "lastExit", "code")
	if !f.IsValid() {
		return 0, false
	}
	b, ok := f.Interface().(uint8)
	return int(b), ok
}

// handlerRunner is the runner that made the handler call hc belongs to (a
// subshell's own inside ( ), $( ), a pipeline or an & job), or nil when the
// field is gone.
func handlerRunner(hc interp.HandlerContext) *interp.Runner {
	f := reflect.ValueOf(&hc).Elem().FieldByName("runner")
	if !f.IsValid() || f.Type() != reflect.TypeFor[*interp.Runner]() {
		return nil
	}
	return (*interp.Runner)(f.UnsafePointer())
}

// runnerJobList is a runner's background jobs as it has them, in the order
// it started them ($! is g1, g2, ...): job n ends as done(n) closes. It
// reads the runner's own list in place, so asking for one job costs the
// same however many the runner started.
type runnerJobList struct {
	procs reflect.Value // the runner's bgProcs, as long as it was when read
}

// runnerJobs is r's background jobs; ok is false when the field is gone.
// Read it only on r's goroutine (a handler call r makes): r appends to the
// list as it starts a job.
func runnerJobs(r *interp.Runner) (jobs runnerJobList, ok bool) {
	procs := runnerField(r, reflect.Slice, "bgProcs")
	if !procs.IsValid() || procs.Type().Elem().Kind() != reflect.Struct {
		return runnerJobList{}, false
	}
	if f, has := procs.Type().Elem().FieldByName("done"); !has || f.Type != reflect.TypeFor[chan struct{}]() {
		return runnerJobList{}, false
	}
	return runnerJobList{procs: procs.Slice(0, procs.Len())}, true
}

// count is how many jobs the runner started.
func (l runnerJobList) count() int {
	if !l.procs.IsValid() {
		return 0
	}
	return l.procs.Len()
}

// done is the channel closed when job n (1-based) ended.
func (l runnerJobList) done(n int) <-chan struct{} {
	f := l.procs.Index(n - 1).FieldByName("done")
	return *(*chan struct{})(f.Addr().UnsafePointer())
}

// all is the channel of every job, in order.
func (l runnerJobList) all() []<-chan struct{} {
	done := make([]<-chan struct{}, l.count())
	for i := range done {
		done[i] = l.done(i + 1)
	}
	return done
}

// runnerField is an unexported field of the runner, by its path from the
// Runner (lastExit, code), settable; the zero Value when a dependency update
// moved it or changed its kind.
func runnerField(r *interp.Runner, kind reflect.Kind, path ...string) reflect.Value {
	f := reflect.ValueOf(r).Elem()
	for _, name := range path {
		if f = f.FieldByName(name); !f.IsValid() {
			return reflect.Value{}
		}
	}
	if f.Kind() != kind {
		return reflect.Value{}
	}
	return reflect.NewAt(f.Type(), f.Addr().UnsafePointer()).Elem()
}

// runnerInFunctionOrSource reports whether r runs a function or a sourced
// file, where a return returns; ok is false when r is nil or a field is
// gone.
func runnerInFunctionOrSource(r *interp.Runner) (in, ok bool) {
	if r == nil {
		return false, false
	}
	inFunc := runnerField(r, reflect.Bool, "inFunc")
	inSource := runnerField(r, reflect.Bool, "inSource")
	if !inFunc.IsValid() || !inSource.IsValid() {
		return false, false
	}
	return inFunc.Bool() || inSource.Bool(), true
}

// runnerErrExit reports whether r has errexit on (set -e). The option's
// place among the runner's options is found once, by setting -e on a
// runner of its own; false when the field is gone or the place is not
// found.
func runnerErrExit(r *interp.Runner) bool {
	i := errExitIndex()
	opts := runnerField(r, reflect.Array, "opts")
	return i >= 0 && opts.IsValid() && i < opts.Len() && opts.Index(i).Bool()
}

// errExitIndex is the index of errexit in a runner's options, -1 when it
// cannot be told: the one option `set -e` turns on.
var errExitIndex = sync.OnceValue(func() int {
	r, err := interp.New()
	if err != nil {
		return -1
	}
	before := runnerField(r, reflect.Array, "opts")
	if !before.IsValid() {
		return -1
	}
	off := make([]bool, before.Len())
	for i := range off {
		off[i] = before.Index(i).Bool()
	}
	if interp.Params("-e")(r) != nil {
		return -1
	}
	found := -1
	for i, was := range off {
		if after := before.Index(i).Bool(); after != was {
			if found >= 0 || !after {
				return -1
			}
			found = i
		}
	}
	return found
})
