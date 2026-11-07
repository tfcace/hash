package main

import (
	"strings"
	"testing"
	"time"
)

// What hash draws before a prompt's input (the notice of a job that ended,
// the info bar) is the prompt's: it comes after the prompt's start mark
// (OSC 133 A), so a terminal that keeps each command's output as a block
// (Rex) does not add it to the last command's. It used to come before the
// line's end mark (OSC 133 D), in the line's output.
func TestInteractive_NoticesComeAfterThePromptStarts(t *testing.T) {
	t.Parallel()
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	s.typeString("sleep 0.1 &\r")
	time.Sleep(800 * time.Millisecond) // the job ends
	from := len(s.output())
	s.typeString("\r")
	s.waitFor(from, "Done", 5*time.Second)
	out := s.output()[from:]
	done := strings.Index(out, "Done")
	start := strings.LastIndex(out[:done], "\x1b]133;A")
	end := strings.LastIndex(out[:done], "\x1b]133;D")
	if start < 0 || start < end {
		t.Errorf("the job's notice came before the prompt started (A at %d, D at %d, notice at %d): %q", start, end, done, out)
	}
	s.typeString("exit\r")
	s.wait(5 * time.Second)
}
