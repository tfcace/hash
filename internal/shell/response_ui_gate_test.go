package shell

import (
	"strings"
	"testing"
	"time"
)

// While the draw gate is closed (e.g. a permission prompt owns the
// terminal) the spinner must not write any frames.
func TestSpinner_DrawGateClosedSuppressesFrames(t *testing.T) {
	var out strings.Builder
	ui := NewResponseUI(&out)
	ui.SetDrawGate(func() bool { return false })

	ui.ShowState(AgentStateThinking)
	time.Sleep(250 * time.Millisecond) // several 80ms ticks
	ui.StopSpinner()

	// OSC 9;4 progress sequences are out-of-band and cursor-neutral; the
	// invariant is that no frame (clear-line + label) reaches the screen.
	if s := out.String(); strings.Contains(s, "\r\x1b[K") || strings.Contains(s, "thinking") {
		t.Fatalf("spinner drew a frame %q while draw gate was closed", s)
	}
}
