package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tfcace/hash/internal/executor"
)

// fatalSignals end a script the way they end bash: the shell's trap for
// the signal and the EXIT trap run, and hash dies of the signal (see
// endShell). SIGHUP is the terminal closing; SIGTERM a polite kill.
// (SIGINT is the interactive shell's own business: the REPL and the
// commands it runs handle Ctrl+C. A script takes it as bash does, see
// watchInterrupts. SIGQUIT ends no shell, as in bash 5.2: see
// dropSignals, watchSignals.)
var fatalSignals = []os.Signal{syscall.SIGHUP, syscall.SIGTERM}

// interactiveFatalSignals end an interactive shell (the terminal's, or
// -i): only SIGHUP. bash ignores SIGTERM there, so a `kill 0` from a job or
// a pipeline's command, which share the shell's process group, or a stray
// `kill -TERM $$`, does not end the terminal's shell.
var interactiveFatalSignals = []os.Signal{syscall.SIGHUP}

// shellKeeps are the signals that end no interactive shell, as bash 5.2
// ignores them there: Ctrl+\ (SIGQUIT), and SIGTERM.
var shellKeeps = []syscall.Signal{syscall.SIGQUIT, syscall.SIGTERM}

// dropSignals makes sigs do nothing to the interactive shell; Go's default
// for them ends hash (with a goroutine dump, for SIGQUIT). hash takes the
// signals and drops them rather than ignoring them: the commands it runs
// would inherit an ignored one, and must still die of Ctrl+\ and of a
// SIGTERM. (The channel is never read: a signal that finds it full is
// dropped.)
func dropSignals(sigs ...syscall.Signal) {
	ch := make(chan os.Signal, 1)
	for _, sig := range sigs {
		signal.Notify(ch, sig)
	}
}

// signalNoter is what watchSignals needs of the executor.
type signalNoter interface {
	WatchSignals(sigs ...syscall.Signal)
	SignalArrived(sig syscall.Signal)
}

// watchSignals makes each of sigs leave a script running (Ctrl+\, as bash
// 5.2 ignores it in every shell; SIGTERM in hash -i, as in bash -i): a
// foreground command dies of it, and the script's trap for it runs once
// the line ended (SignalArrived). It is taken, not ignored, as in
// dropSignals; one hash was started ignoring stays ignored.
func watchSignals(e signalNoter, sigs ...syscall.Signal) {
	ch := make(chan os.Signal, 1)
	for _, sig := range sigs {
		if !signal.Ignored(sig) {
			e.WatchSignals(sig)
			signal.Notify(ch, sig)
		}
	}
	go func() {
		for got := range ch {
			e.SignalArrived(got.(syscall.Signal))
		}
	}()
}

// signalError is a shell context's cause once a fatal signal arrived.
type signalError struct{ sig syscall.Signal }

func (e signalError) Error() string { return e.sig.String() }

// Signal names the signal, for the executor to send the running command.
func (e signalError) Signal() syscall.Signal { return e.sig }

// signalContext is a context for hash's lifetime that ends when one of
// the fatal signals arrives, with the signal as its cause, or when end is
// called for a signal hash handles itself. A SIGHUP hash was started
// ignoring stays ignored (nohup), as in bash; Go keeps the ignore only for
// SIGHUP and SIGINT, so a SIGTERM ignored at the start ends hash
// all the same. A signal the shell ignores when it arrives (ignores, nil
// for none: an empty trap, trap "" TERM) does not end it.
func signalContext(fatal []os.Signal, ignores func(syscall.Signal) bool) (ctx context.Context, end func(syscall.Signal)) {
	ctx, cancel := context.WithCancelCause(context.Background())
	end = func(sig syscall.Signal) { cancel(signalError{sig}) }
	ch := make(chan os.Signal, 1)
	for _, sig := range fatal {
		if !signal.Ignored(sig) {
			signal.Notify(ch, sig)
		}
	}
	go func() {
		for {
			select {
			case got := <-ch:
				sig := got.(syscall.Signal)
				if ignores == nil || !ignores(sig) {
					end(sig)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return ctx, end
}

// interrupter is what watchInterrupts needs of the executor.
type interrupter interface {
	InterruptLine(end func()) bool
	WatchInterrupts()
	IgnoresSignal(sig syscall.Signal) bool
}

// watchInterrupts makes SIGINT end a script as it ends bash: Ctrl+C at the
// terminal sends it to the foreground commands too, and the one running
// decides. When it dies of the signal, or none runs, the script ends as on
// a fatal signal (end: the EXIT trap, then hash dies of SIGINT); a command
// that handles it lets the script go on. A SIGINT hash was started
// ignoring (a background job of another script) stays ignored.
func watchInterrupts(e interrupter, end func(syscall.Signal)) {
	if signal.Ignored(syscall.SIGINT) {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT)
	e.WatchInterrupts()
	interrupted := func() { end(syscall.SIGINT) }
	go func() {
		for range ch {
			if e.IgnoresSignal(syscall.SIGINT) {
				continue // trap '' INT
			}
			if !e.InterruptLine(interrupted) {
				interrupted()
			}
		}
	}()
}

// shellCloser closes a shell as hash ends.
type shellCloser struct {
	// signalTrap runs the shell's trap for the fatal signal that ends it,
	// with $? = status, and reports the status an exit in it set, if one
	// did. nil for a shell that keeps no signal traps.
	signalTrap func(sig syscall.Signal, status int) (code int, exited bool)
	// close runs the shell's EXIT trap with $? = status and reports the
	// status an exit in the trap set, if one did. sig is the fatal signal
	// that ended the shell, 0 for none: endOf's one reading of it, which
	// the closer must not make again (one that arrived since would not
	// match status).
	close func(sig syscall.Signal, status int) (code int, exited bool)
}

// shellEnd is how hash ends: it exits with status, or dies of signal when
// signal is not 0.
type shellEnd struct {
	status int
	signal syscall.Signal
}

// endOf closes a shell that stopped with status and says how hash ends,
// the same for a script and the interactive shell. After a fatal signal
// (ctx's cause) the shell's trap for it runs, then the EXIT trap, both with
// $? = 128+signal, and hash dies of the signal whatever the EXIT trap does,
// as bash 5 does. An exit in the signal's trap ends the shell as an exit
// anywhere does. Otherwise hash exits with status, or with the status of
// an exit in the EXIT trap.
func endOf(ctx context.Context, status int, sh shellCloser) shellEnd {
	sig, signaled := executor.SignalCause(ctx)
	if signaled {
		status = 128 + int(sig)
		code, exited := 0, false
		if sh.signalTrap != nil {
			code, exited = sh.signalTrap(sig, status)
		}
		if !exited {
			sh.close(sig, status)
			return shellEnd{status: status, signal: sig}
		}
		status, sig = code, 0
	}
	if code, exited := sh.close(sig, status); exited {
		status = code
	}
	if sig, ok := executor.SignalCause(ctx); ok && !signaled { // it came while the shell closed
		return shellEnd{status: 128 + int(sig), signal: sig}
	}
	return shellEnd{status: status}
}

// endShell closes the shell (endOf) and ends hash.
func endShell(ctx context.Context, status int, sh shellCloser) {
	end := endOf(ctx, status, sh)
	// Die of it: the default action, now that hash no longer handles it.
	// The exit is for a signal that does not arrive.
	if end.signal != 0 {
		signal.Reset(end.signal)
		_ = syscall.Kill(os.Getpid(), end.signal)
		time.Sleep(time.Second)
	}
	os.Exit(end.status)
}
