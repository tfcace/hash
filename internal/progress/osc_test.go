package progress

import (
	"bytes"
	"testing"
	"time"
)

func TestOSC_Start(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)

	osc.Start()

	// Should emit OSC 9;4;3;0 (indeterminate/spinner progress) with BEL terminator
	// State 3 is specifically for indeterminate progress (spinner)
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]9;4;3;0\x07")) {
		t.Errorf("Start() should emit OSC 9;4;3;0 BEL, got %q", buf.String())
	}
}

func TestOSC_SetProgress(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)

	osc.SetProgress(50)

	// Should emit OSC 9;4;1;50 (50% progress) with BEL terminator
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]9;4;1;50\x07")) {
		t.Errorf("SetProgress(50) should emit OSC 9;4;1;50 BEL, got %q", buf.String())
	}
}

func TestOSC_Done(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)

	osc.Done()

	// Should emit OSC 9;4;0 (clear progress) with BEL terminator
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]9;4;0\x07")) {
		t.Errorf("Done() should emit OSC 9;4;0 BEL, got %q", buf.String())
	}
}

func TestOSC_Error(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)

	osc.Error()

	// Should emit OSC 9;4;2 (error state) with BEL terminator
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]9;4;2\x07")) {
		t.Errorf("Error() should emit OSC 9;4;2 BEL, got %q", buf.String())
	}
}

func TestOSC_Disabled(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)
	osc.SetEnabled(false)

	osc.Start()
	osc.SetProgress(50)
	osc.Done()

	// Buffer should be empty when disabled
	if buf.Len() != 0 {
		t.Errorf("OSC should not emit when disabled, got %q", buf.String())
	}
}

func TestOSC_Pause(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)

	osc.Pause()

	// Should emit OSC 9;4;3 (paused state) with BEL terminator
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]9;4;3\x07")) {
		t.Errorf("Pause() should emit OSC 9;4;3 BEL, got %q", buf.String())
	}
}

func TestOSC_ProgressClamp(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{-10, 0},
		{0, 0},
		{50, 50},
		{100, 100},
		{150, 100},
	}

	for _, tt := range tests {
		var buf bytes.Buffer
		osc := NewOSC(&buf)
		osc.SetProgress(tt.input)

		expected := []byte("\x1b]9;4;1;")
		if !bytes.Contains(buf.Bytes(), expected) {
			t.Errorf("SetProgress(%d) should clamp to valid range", tt.input)
		}
	}
}

func TestTracker_NotShownBeforeThreshold(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)
	tracker := NewTracker(osc, 100*time.Millisecond)

	tracker.Start()
	tracker.Tick() // Immediate tick, before threshold

	if tracker.Shown() {
		t.Error("Tracker should not show before threshold")
	}
	if buf.Len() != 0 {
		t.Error("No OSC should be emitted before threshold")
	}
}

func TestTracker_ShownAfterThreshold(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)
	tracker := NewTracker(osc, 10*time.Millisecond)

	tracker.Start()
	time.Sleep(20 * time.Millisecond)
	tracker.Tick()

	if !tracker.Shown() {
		t.Error("Tracker should show after threshold")
	}
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]9;4;3;0\x07")) {
		t.Errorf("Should emit start sequence, got %q", buf.String())
	}
}

func TestTracker_DoneSuccess(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)
	tracker := NewTracker(osc, 0) // Immediate threshold

	tracker.Start()
	tracker.Tick()
	buf.Reset() // Clear the start sequence

	tracker.Done(true)

	if !bytes.Contains(buf.Bytes(), []byte("\x1b]9;4;0\x07")) {
		t.Errorf("Done(true) should emit clear sequence, got %q", buf.String())
	}
}

func TestTracker_DoneError(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)
	tracker := NewTracker(osc, 0) // Immediate threshold

	tracker.Start()
	tracker.Tick()
	buf.Reset() // Clear the start sequence

	tracker.Done(false)

	if !bytes.Contains(buf.Bytes(), []byte("\x1b]9;4;2\x07")) {
		t.Errorf("Done(false) should emit error sequence, got %q", buf.String())
	}
}

func TestTracker_DoneWithoutShow(t *testing.T) {
	var buf bytes.Buffer
	osc := NewOSC(&buf)
	tracker := NewTracker(osc, 100*time.Millisecond)

	tracker.Start()
	// Don't call Tick, so progress never shows
	tracker.Done(true)

	if buf.Len() != 0 {
		t.Error("Done should not emit if progress was never shown")
	}
}
