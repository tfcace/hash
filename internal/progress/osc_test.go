package progress

import (
	"bytes"
	"testing"
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
	osc.Error()
	osc.Done()

	// Buffer should be empty when disabled
	if buf.Len() != 0 {
		t.Errorf("OSC should not emit when disabled, got %q", buf.String())
	}
}
