package markdown

import (
	"strings"
	"testing"
)

// The line phase: a start too short to decide is held by Flush, a flushed
// fragment opens the line, the newline (or Finish) starts the next fresh.
func TestStreaming_LinePhase(t *testing.T) {
	r := NewStreamingRenderer()
	r.Write("#")
	if out := r.Flush(); out != "" || r.phase != lineOpening {
		t.Fatalf("held start: out %q phase %v", out, r.phase)
	}
	r.Write("# Title")
	r.Write("\nplain")
	if r.phase != lineOpening {
		t.Fatalf("after a newline: phase %v, want lineOpening", r.phase)
	}
	if out := r.Flush(); out != "plain" || r.phase != lineOpen {
		t.Fatalf("flushed text: out %q phase %v", out, r.phase)
	}
	// A continuation is never read as a line start.
	if out := r.Write("# not a header\n"); strings.Contains(out, bold) || r.phase != lineOpening {
		t.Fatalf("continuation: out %q phase %v", out, r.phase)
	}
	r.Write("```\ncode")
	r.Flush()
	if out := r.Finish(); !strings.HasSuffix(out, reset) || r.phase != lineOpening || r.inCodeBlock {
		t.Fatalf("finish mid code line: out %q phase %v code %v", out, r.phase, r.inCodeBlock)
	}
}
