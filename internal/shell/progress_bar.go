package shell

import (
	"io"
	"sync"
	"time"

	"github.com/tfcace/hash/internal/progress"
)

// progressFailHold is how long a failure's red badge stays before it clears.
const progressFailHold = 500 * time.Millisecond

// progressBar is hash's own OSC 9;4 indicator, shared by ?? turns and model
// update. A failure shows the red badge briefly, then clears it; whatever
// claims the bar in the meantime cancels that pending clear, so it cannot
// wipe a newer indicator. Methods are safe on a nil receiver.
type progressBar struct {
	mu      sync.Mutex
	osc     *progress.OSC
	pending *time.Timer // clears a failure badge; nil when none is pending
}

func newProgressBar(out io.Writer) *progressBar {
	return &progressBar{osc: progress.NewOSC(out)}
}

// start shows the indeterminate indicator.
func (p *progressBar) start() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelPendingLocked()
	p.osc.Start()
}

// done clears the indicator.
func (p *progressBar) done() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelPendingLocked()
	p.osc.Done()
}

// fail shows the error badge and clears it after progressFailHold, the way
// progress.Tracker does; a bare Error() would leave the badge red.
func (p *progressBar) fail() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelPendingLocked()
	p.osc.Error()
	var t *time.Timer
	t = time.AfterFunc(progressFailHold, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.pending == t {
			p.pending = nil
			p.osc.Done()
		}
	})
	p.pending = t
}

func (p *progressBar) cancelPendingLocked() {
	if p.pending != nil {
		p.pending.Stop()
		p.pending = nil
	}
}
