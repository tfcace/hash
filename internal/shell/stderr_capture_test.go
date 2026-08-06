package shell

import (
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStderrCaptureRetainsRollingUTF8SafeTail(t *testing.T) {
	capture := newStderrCapture(io.Discard)
	prefix := "old" + strings.Repeat("x", maxStderrCapture)
	_, _ = capture.Write([]byte(prefix))
	_, _ = capture.Write([]byte(strings.Repeat("界", 100) + "latest"))
	got := capture.String()
	if len(got) > maxStderrCapture || !utf8.ValidString(got) {
		t.Fatalf("invalid bounded tail: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	if !strings.HasSuffix(got, "latest") || strings.Contains(got, "old") {
		t.Fatalf("capture did not retain latest bytes")
	}
}
