package shell

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The spinner is the cursor's row or it is not: showing it again while it
// runs only changes its text, taking the row stops it and erases its row
// once, and showing it after that starts a fresh run.
func TestSpinner_ShowAndTakeRowFollowTheRun(t *testing.T) {
	var screen lockedBuffer
	aoc := NewAgentOutputCoordinator(&screen)
	aoc.ShowState(AgentStateThinking)
	first := aoc.spinner
	aoc.ShowActivity("agent · running · ls")
	if aoc.spinner != first || first.text != "agent · running · ls" || rowOf(aoc) != rowSpinner {
		t.Fatal("showing a running spinner started another run")
	}
	aoc.TakeRow()
	select {
	case <-first.stop:
	default:
		t.Fatal("taking the row did not stop the spinner")
	}
	if aoc.spinner != nil || rowOf(aoc) != rowFree {
		t.Fatalf("after TakeRow: spinner %v, row %v, want none on a free row", aoc.spinner, rowOf(aoc))
	}
	aoc.ShowState(AgentStateThinking)
	if aoc.spinner == nil || aoc.spinner == first {
		t.Fatal("showing a stopped spinner did not start a fresh run")
	}
	drawn := len(screen.String())
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(aoc.TakeRow)
	}
	wg.Wait()
	if n := strings.Count(screen.String()[drawn:], "\r\x1b[K"); n != 1 {
		t.Errorf("two racing TakeRows erased the spinner's row %d times, want once", n)
	}
}

// A stopped spinner draws no more frames: the row it left is the next
// row's, and a late frame would draw over it.
func TestSpinner_DrawsNothingOnceTheRowIsTaken(t *testing.T) {
	t.Parallel()
	var screen lockedBuffer
	aoc := NewAgentOutputCoordinator(&screen)
	aoc.ShowState(AgentStateThinking)
	waitFor(t, func() bool { return screen.String() != "" }) // a frame
	aoc.TakeRow()
	drawn := len(screen.String())
	time.Sleep(200 * time.Millisecond) // time enough for a late frame
	if after := screen.String()[drawn:]; after != "" {
		t.Errorf("the spinner drew %q after its row was taken", after)
	}
}

func TestSpinner_ShowsEachState(t *testing.T) {
	t.Parallel()
	for _, state := range []AgentState{AgentStateConnecting, AgentStateSending, AgentStateThinking, AgentStateReceiving} {
		var buf lockedBuffer
		aoc := NewAgentOutputCoordinator(&buf)
		aoc.ShowState(state)
		// A frame is drawn on a timer: wait for it, however late it is.
		waitFor(t, func() bool { return strings.Contains(buf.String(), state.String()) })
		aoc.TakeRow()
	}
}

// A tool row interrupts the spinner many times in a turn. The animation
// carries on from where it was, or the name would never finish resolving.
func TestSpinner_CarriesOnWhereItWasAfterARowInterruptsIt(t *testing.T) {
	t.Parallel()
	var screen lockedBuffer
	aoc := NewAgentOutputCoordinator(&screen)
	aoc.ShowState(AgentStateThinking)
	aoc.mu.Lock()
	aoc.spinnerFrame = spinnerFirstLetter + 3*spinnerLetterStep // the whole name is settled
	aoc.mu.Unlock()
	aoc.TakeRow()
	drawn := len(screen.String())
	aoc.ShowState(AgentStateThinking)
	waitFor(t, func() bool { return screen.String()[drawn:] != "" }) // a frame
	aoc.TakeRow()
	if after := stripANSI(screen.String()[drawn:]); !strings.Contains(after, " hash agent · thinking") {
		t.Errorf("the restarted spinner drew %q, want the settled name", after)
	}
}

func TestSpinner_StartsFromNoiseOnANewTurn(t *testing.T) {
	var screen lockedBuffer
	aoc := NewAgentOutputCoordinator(&screen)
	aoc.spinnerFrame = 30
	aoc.BeginTurn()
	if aoc.spinnerFrame != 0 {
		t.Errorf("a new turn starts the spinner at frame %d, want 0", aoc.spinnerFrame)
	}
}
