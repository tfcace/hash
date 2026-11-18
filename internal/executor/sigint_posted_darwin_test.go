package executor

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
)

// The system knows a SIGINT was sent as soon as it was, before Go's
// delivery, and each one is told once.
func TestSigintPosted_KnowsASIGINTAsSoonAsItIsSent(t *testing.T) {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGINT) // or the signal would end the test
	defer signal.Stop(ch)
	posted := sigintPosted()
	if posted == nil {
		t.Fatal("no way to ask on this system")
	}
	posted() // forget what other tests sent
	if posted() {
		t.Fatal("a SIGINT is reported with none sent")
	}
	for range 200 {
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		if !posted() {
			t.Fatal("a SIGINT sent was not known at once")
		}
		if posted() {
			t.Fatal("one SIGINT was reported twice")
		}
		<-ch
	}
}
