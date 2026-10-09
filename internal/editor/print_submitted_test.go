package editor

import (
	"bytes"
	"strings"
	"testing"
)

// PrintSubmitted leaves a line on screen the way Run does after Enter, so a
// command the shell runs on the user's behalf reads like one they typed.
func TestPrintSubmitted_RendersLikeASubmittedLine(t *testing.T) {
	var out bytes.Buffer
	ed := New(Config{Gutter: true, Prompt: "❯ "}, strings.NewReader(""), &out)

	ed.PrintSubmitted("echo hi")

	got := out.String()
	if want := "│❯ \x1b[1mecho hi\x1b[0m"; !strings.Contains(got, want) {
		t.Errorf("PrintSubmitted() = %q, want it to contain %q", got, want)
	}
	if !strings.HasSuffix(got, "\r\n") {
		t.Errorf("PrintSubmitted() = %q, want it to end the line", got)
	}
}
