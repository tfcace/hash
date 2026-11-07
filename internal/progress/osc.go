package progress

import (
	"fmt"
	"io"
)

// OSC provides terminal progress bar support via OSC 9;4.
// Supported by: iTerm2, Windows Terminal, WezTerm, Kitty, Ghostty
//
// Sequence format: ESC ] 9 ; 4 ; <state> [; <progress>] ST
// States (hash sends 0, 2 and 3):
//
//	0 = remove progress
//	1 = set progress (0-100)
//	2 = error state
//	3 = indeterminate (spinning indicator)
//	4 = paused (warning) state
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

// Enabled reports whether sequences are written.
func (o *OSC) Enabled() bool {
	return o.enabled
}

// Start begins indeterminate progress (spinning indicator).
func (o *OSC) Start() {
	if !o.enabled {
		return
	}
	// OSC 9;4;3 = indeterminate progress. State 1 requires a progress
	// value; state 3 ignores it.
	fmt.Fprintf(o.out, "\x1b]9;4;3;0"+oscST)
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
