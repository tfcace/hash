package completion

import (
	"context"
	"testing"
	"time"
)

// slowCommand honors its context and answers after d.
func slowCommand(d time.Duration, lines ...string) func(ctx context.Context, name string, args ...string) ([]string, error) {
	return func(ctx context.Context, name string, args ...string) ([]string, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
			return lines, nil
		}
	}
}

// handlerCompleter adapts a CommandHandler to the Completer interface for
// tabUntil.
type handlerCompleter struct {
	h    CommandHandler
	args []string
}

func (c handlerCompleter) Name() string { return "handler" }
func (c handlerCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	return c.h.Complete(ctx, c.args, ""), nil
}

// `brew list` routinely takes longer than one Tab's budget. Bounding it by
// that budget meant it never finished; detached, it fills the cache and a
// later Tab answers.
func TestBrewHandler_SlowListEventuallyAnswers(t *testing.T) {
	h := NewBrewHandler()
	h.runCommand = slowCommand(300*time.Millisecond, "wget")
	if _, ok := tabUntil(t, handlerCompleter{h: h, args: []string{"uninstall"}}, "", "wget", 1500*time.Millisecond); !ok {
		t.Fatal("installed packages from a slow brew list never appeared")
	}
}

// Same for `pip freeze`, which takes hundreds of milliseconds.
func TestPipHandler_SlowFreezeEventuallyAnswers(t *testing.T) {
	h := NewPipHandler("pip")
	h.runCommand = slowCommand(300*time.Millisecond, "requests==2.0")
	if _, ok := tabUntil(t, handlerCompleter{h: h, args: []string{"uninstall"}}, "", "requests", 1500*time.Millisecond); !ok {
		t.Fatal("installed packages from a slow pip freeze never appeared")
	}
}

// While the slow source runs, the handler claims the argument as pending.
func TestBrewHandler_SlowListIsPending(t *testing.T) {
	h := NewBrewHandler()
	h.runCommand = slowCommand(time.Second, "wget")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if got := h.Complete(ctx, []string{"uninstall"}, "").Outcome(); got != OutcomePending {
		t.Fatalf("outcome = %v, want pending", got)
	}
}
