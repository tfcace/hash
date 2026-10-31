package shell

import (
	"bytes"
	"testing"
)

// The executor looks through the capture to see the terminal behind it.
func TestStderrCapture_UnwrapsToOriginal(t *testing.T) {
	var original bytes.Buffer
	if got := newStderrCapture(&original).Unwrap(); got != &original {
		t.Errorf("Unwrap() = %v, want the original writer", got)
	}
}
