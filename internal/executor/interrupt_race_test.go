package executor

import (
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// Ctrl+C sends SIGINT to hash and its foreground command at once, but Go
// hands hash its signal on another goroutine: the command's death can be
// seen first. A command that died of SIGINT must still end the shell when
// the shell's own SIGINT lands a moment later, not let the next command run.
func TestForeground_SIGINTThatLandsAfterTheCommandDiedStillEnds(t *testing.T) {
	cmd := exec.Command("sh", "-c", "kill -INT $$")
	_ = cmd.Run()
	if !diedOf(cmd.ProcessState, 2) {
		t.Fatalf("setup: %v did not die of SIGINT", cmd.ProcessState)
	}

	f := foreground{watched: true}
	var ended atomic.Bool
	f.start()
	done := make(chan struct{})
	go func() {
		f.end(cmd.ProcessState)
		close(done)
	}()
	// The shell's SIGINT, late: once the death was seen.
	for f.state() != batchSIGINTDeathSeen {
		time.Sleep(time.Millisecond)
	}
	if taken, _ := f.interrupt(func() { ended.Store(true) }); !taken {
		t.Fatal("the late SIGINT found no command deciding: the shell would end at once")
	}
	<-done
	if !ended.Load() {
		t.Fatal("the command died of the SIGINT and end returned before the shell's SIGINT ended it")
	}
}

// A command that kills itself with SIGINT, with no Ctrl+C, lets the script
// go on after the short wait.
func TestForeground_ACommandsOwnSIGINTDoesNotEnd(t *testing.T) {
	cmd := exec.Command("sh", "-c", "kill -INT $$")
	_ = cmd.Run()
	f := foreground{watched: true}
	f.start()
	start := time.Now()
	f.end(cmd.ProcessState)
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("end waited %s for a SIGINT that never came", waited)
	}
	if f.state() != batchIdle {
		t.Fatalf("state %v after the wait, want idle", f.state())
	}
}

// A command that starts while the batch's last command waits for a late
// SIGINT (the other side of a pipeline runs in hash, a loop) joins the
// batch: the wait must not end the batch under it.
func TestForeground_ACommandStartingDuringTheWaitJoinsTheBatch(t *testing.T) {
	killed := endedProcess(t, "kill -INT $$")
	handled := endedProcess(t, "exit 0")
	f := foreground{watched: true}
	f.start()
	done := make(chan struct{})
	go func() {
		f.end(killed.ProcessState)
		close(done)
	}()
	for f.state() != batchSIGINTDeathSeen {
		time.Sleep(time.Millisecond)
	}
	f.start()
	<-done
	if f.state() == batchIdle {
		t.Fatal("the batch ended with a command running")
	}
	ends := 0
	f.interrupt(func() { ends++ })
	f.end(handled.ProcessState)
	if ends != 0 || f.state() != batchIdle {
		t.Fatalf("ends %d, state %v; want 0 (the command handled it) and idle", ends, f.state())
	}
}

// Nobody takes the SIGINT of an executor whose caller does not route it
// (WatchInterrupts): no late SIGINT can come, so a command that dies of
// one costs no wait. Every such death used to cost lateSIGINT, in the
// interactive shell too.
func TestForeground_UnwatchedDoesNotWaitForALateSIGINT(t *testing.T) {
	killed := endedProcess(t, "kill -INT $$")
	e := New()
	e.foreground.start()
	start := time.Now()
	e.foreground.end(killed.ProcessState)
	if waited := time.Since(start); waited >= lateSIGINT {
		t.Fatalf("end waited %s for a SIGINT nobody routes", waited)
	}

	e.WatchInterrupts()
	e.foreground.start()
	start = time.Now()
	e.foreground.end(killed.ProcessState)
	if waited := time.Since(start); waited < lateSIGINT {
		t.Fatalf("end waited %s once SIGINT is routed, want lateSIGINT", waited)
	}
}

// Ctrl+C reaches the command and the shell at once, but a command that
// handles it and exits at once can end before Go hands the shell its
// SIGINT: the SIGINT that lands a moment after is the ended batch's, which
// handled it, and must not end the shell. A script under load used to die
// of a Ctrl+C its command had handled.
func TestForeground_SIGINTThatLandsJustAfterAHandlerEndedIsItsOwn(t *testing.T) {
	handled := endedProcess(t, "exit 3")
	f := foreground{watched: true}
	f.start()
	f.end(handled.ProcessState) // it handled the SIGINT that is on its way
	ends := 0
	taken, trapDue := f.interrupt(func() { ends++ })
	if !taken {
		t.Fatal("the late SIGINT found no batch: the shell would end now")
	}
	if ends != 0 || !trapDue {
		t.Fatalf("ends %d; want 0, and the INT trap due", ends)
	}
	if taken, _ := f.interrupt(func() { ends++ }); taken {
		t.Fatal("a second SIGINT was taken by the ended batch too")
	}
}
