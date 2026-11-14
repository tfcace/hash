package shell

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// interrupts owns SIGINT for the shell's lifetime. Hash never dies of
// Ctrl+C: a command in the foreground gets the terminal's SIGINT itself, and
// in-process work that should stop on Ctrl+C (a ?? turn, a builtin waiting on
// the agent) claims the signal for as long as it runs. Claims nest: the most
// recent one gets the signal, and releasing it hands the signal back. The zero
// value is ready to use; without listen it still routes fire.
type interrupts struct {
	mu     sync.Mutex
	claims []*interruptClaim
}

type interruptClaim struct{ onInterrupt func() }

// listen subscribes to SIGINT until the returned stop is called.
func (r *interrupts) listen() (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				r.fire()
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// claim routes SIGINT to onInterrupt until release is called. Release is
// idempotent and removes this claim wherever it sits in the stack.
func (r *interrupts) claim(onInterrupt func()) (release func()) {
	c := &interruptClaim{onInterrupt: onInterrupt}
	r.mu.Lock()
	r.claims = append(r.claims, c)
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for i, other := range r.claims {
			if other == c {
				r.claims = append(r.claims[:i], r.claims[i+1:]...)
				return
			}
		}
	}
}

// fire delivers one interrupt to the newest claim, or drops it when nothing
// in hash is waiting to be interrupted.
func (r *interrupts) fire() {
	r.mu.Lock()
	var fn func()
	if n := len(r.claims); n > 0 {
		fn = r.claims[n-1].onInterrupt
	}
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// interruptible derives a context that Ctrl+C cancels, for in-process work
// that may block (a builtin waiting on the agent or npm). Call done when the
// work ends.
func (r *interrupts) interruptible(parent context.Context) (ctx context.Context, done func()) {
	ctx, cancel := context.WithCancel(parent)
	release := r.claim(cancel)
	return ctx, func() {
		release()
		cancel()
	}
}
