package shell

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
)

func TestInterrupts_FireWithoutAClaimIsDropped(t *testing.T) {
	var r interrupts
	r.fire() // nothing claims it: no panic, nothing happens
}

func TestInterrupts_NewestClaimGetsTheSignal(t *testing.T) {
	var r interrupts
	var got []string
	releaseOuter := r.claim(func() { got = append(got, "outer") })
	releaseInner := r.claim(func() { got = append(got, "inner") })

	r.fire()
	releaseInner()
	r.fire()
	releaseInner() // idempotent: must not drop the outer claim
	r.fire()
	releaseOuter()
	r.fire()

	want := []string{"inner", "outer", "outer"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestInterrupts_ReleasingAnOlderClaimKeepsTheNewest(t *testing.T) {
	var r interrupts
	var got string
	releaseOuter := r.claim(func() { got = "outer" })
	defer r.claim(func() { got = "inner" })()
	releaseOuter()
	r.fire()
	if got != "inner" {
		t.Errorf("fire went to %q, want the remaining inner claim", got)
	}
}

// A real SIGINT while hash listens is routed, never fatal: without listen
// this test binary would die of the signal, as hash did when Ctrl+C reached
// it during a command run with the terminal cooked (`sleep 3 > /dev/null`).
func TestInterrupts_ListenSurvivesARealSIGINT(t *testing.T) {
	var r interrupts
	stop := r.listen()
	defer stop()

	// Unclaimed: dropped, and the process lives on.
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	got := make(chan struct{}, 1)
	defer r.claim(func() { got <- struct{}{} })()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("claimed SIGINT never reached its handler")
	}
}

// blockingModelTransport stands in for an agent that never answers the model
// query, the way a hung adapter would.
type blockingModelTransport struct {
	namedTestTransport
	entered chan struct{}
}

func (t blockingModelTransport) EnsureModelInfo(ctx context.Context) error {
	close(t.entered)
	<-ctx.Done()
	return ctx.Err()
}

// Ctrl+C while `model` waits on the agent ends the builtin, not the shell.
func TestExecuteBuiltin_CtrlCCancelsABuiltinWaitingOnTheAgent(t *testing.T) {
	tr := blockingModelTransport{entered: make(chan struct{})}
	sh := &Shell{config: config.Default(), agentHandler: NewAgentHandler(agent.NewClient(tr))}

	errCh := make(chan error, 1)
	go func() {
		_, err := sh.executeBuiltin(context.Background(), "model")
		errCh <- err
	}()
	<-tr.entered
	sh.interrupts.fire()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("model error = %v, want it canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("model kept waiting on the agent after Ctrl+C")
	}
}
