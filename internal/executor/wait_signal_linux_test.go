package executor

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A wait for the terminal goes on when a signal interrupts it: select
// fails with EINTR when a signal lands on its thread (Linux never restarts
// select). The wait used to end there, as if no data came, so the drain of
// terminal replies could stop early and leave the fence's reply for the
// command. The signal goes to the waiting thread itself (tgkill), which
// only Linux offers.
func TestHasDataAvailable_GoesOnAfterASignal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGUSR1)
	defer signal.Stop(sigs)
	tid := make(chan int, 1)
	got := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid <- unix.Gettid()
		got <- hasDataAvailable(int(r.Fd()), 2*time.Second)
	}()
	waiter := <-tid
	time.Sleep(50 * time.Millisecond) // in the select
	_ = unix.Tgkill(os.Getpid(), waiter, syscall.SIGUSR1)
	time.AfterFunc(150*time.Millisecond, func() { _, _ = w.WriteString("x") })
	if !<-got {
		t.Fatal("the wait ended at a signal, before the data came")
	}
}
