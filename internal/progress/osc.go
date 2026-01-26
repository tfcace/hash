package progress

import (
	"fmt"
	"io"
	"time"
)

// OSC provides terminal progress bar support via OSC 9;4.
// Supported by: iTerm2, Windows Terminal, WezTerm, Kitty, Ghostty
//
// Sequence format: ESC ] 9 ; 4 ; <state> [; <progress>] ST
// States:
//
//	0 = remove progress
//	1 = set progress (0-100) or indeterminate if no value
//	2 = error state
//	3 = paused state
//
// ST (String Terminator) can be BEL (\x07) or ESC \ (\x1b\x5c).
// We use BEL as it's more widely supported.
type OSC struct {
	out     io.Writer
	enabled bool
}

// oscST is the String Terminator - BEL is more widely supported
const oscST = "\x07"

// NewOSC creates a new OSC progress handler.
func NewOSC(out io.Writer) *OSC {
	return &OSC{
		out:     out,
		enabled: true,
	}
}

// SetEnabled enables or disables OSC output.
func (o *OSC) SetEnabled(enabled bool) {
	o.enabled = enabled
}

// Start begins indeterminate progress (spinning indicator).
func (o *OSC) Start() {
	if !o.enabled {
		return
	}
	// OSC 9;4;3 = indeterminate progress (state 3 is the spinner)
	// State 1 requires a progress value, state 3 ignores it
	fmt.Fprintf(o.out, "\x1b]9;4;3;0"+oscST)
}

// SetProgress sets progress to a percentage (0-100).
func (o *OSC) SetProgress(percent int) {
	if !o.enabled {
		return
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	fmt.Fprintf(o.out, "\x1b]9;4;1;%d"+oscST, percent)
}

// Done clears the progress indicator.
func (o *OSC) Done() {
	if !o.enabled {
		return
	}
	// OSC 9;4;0 = remove progress
	fmt.Fprintf(o.out, "\x1b]9;4;0"+oscST)
}

// Error shows error state in the progress indicator.
func (o *OSC) Error() {
	if !o.enabled {
		return
	}
	// OSC 9;4;2 = error state
	fmt.Fprintf(o.out, "\x1b]9;4;2"+oscST)
}

// Pause shows paused state.
func (o *OSC) Pause() {
	if !o.enabled {
		return
	}
	// OSC 9;4;3 = paused state
	fmt.Fprintf(o.out, "\x1b]9;4;3"+oscST)
}

// Tracker wraps command execution with progress indication.
type Tracker struct {
	osc       *OSC
	threshold time.Duration // Only show progress after this duration
	started   time.Time
	shown     bool
}

// NewTracker creates a new progress tracker.
func NewTracker(osc *OSC, threshold time.Duration) *Tracker {
	return &Tracker{
		osc:       osc,
		threshold: threshold,
	}
}

// Start begins tracking.
func (t *Tracker) Start() {
	t.started = time.Now()
	t.shown = false
}

// Tick should be called periodically during command execution.
// Shows progress bar after threshold is exceeded.
func (t *Tracker) Tick() {
	if t.shown {
		return
	}
	if time.Since(t.started) >= t.threshold {
		t.osc.Start()
		t.shown = true
	}
}

// Done completes tracking with success or error.
func (t *Tracker) Done(success bool) {
	if !t.shown {
		return
	}
	if success {
		t.osc.Done()
	} else {
		t.osc.Error()
		// Clear after a short delay so user sees the error state
		time.AfterFunc(500*time.Millisecond, func() {
			t.osc.Done()
		})
	}
}

// Shown returns whether the progress bar was shown.
func (t *Tracker) Shown() bool {
	return t.shown
}
