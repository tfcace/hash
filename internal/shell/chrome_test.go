package shell

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/tfcace/hash/internal/agent"
)

// withColumns pretends the pane is cols wide for the rest of the test.
func withColumns(t *testing.T, cols int) {
	t.Helper()
	old := terminalColumns
	terminalColumns = func() int { return cols }
	t.Cleanup(func() { terminalColumns = old })
}

// assertRowsFit fails when any row of out is as wide as the pane: the
// terminal would wrap it, and erasing by row count would leave a piece.
func assertRowsFit(t *testing.T, out string, cols int) {
	t.Helper()
	for _, row := range strings.Split(stripANSI(out), "\n") {
		row = row[strings.LastIndex(row, "\r")+1:] // what \r overwrote is gone
		if w := runewidth.StringWidth(row); w >= cols {
			t.Errorf("row %q is %d columns in a %d-column pane", row, w, cols)
		}
	}
}

func TestFitHint(t *testing.T) {
	hints := []string{"enter to finish", "tab to copy", "r to reply"}
	tests := []struct {
		width int
		want  string
	}{
		{0, "  agent · enter to finish · tab to copy · r to reply"},
		{80, "  agent · enter to finish · tab to copy · r to reply"},
		// The scope label goes before any key does.
		{50, "  enter to finish · tab to copy · r to reply"},
		// Then whole hints from the end, never half a word.
		{30, "  enter to finish"},
	}
	for _, tt := range tests {
		if got := fitHint("  ", "agent", hints, tt.width); got != tt.want {
			t.Errorf("fitHint(width %d) = %q, want %q", tt.width, got, tt.want)
		}
	}
}

func TestFitText(t *testing.T) {
	if got := fitText("mkdir -p /tmp/a-long-name", 10); got != "mkdir -p …" {
		t.Errorf("fitText = %q", got)
	}
	if got := fitText("short", 10); got != "short" {
		t.Errorf("fitText = %q", got)
	}
	if got := fitText("anything at all", 0); got != "anything at all" {
		t.Errorf("fitText with no width = %q", got)
	}
}

// Each 80ms frame rewrites the spinner row with \r: a frame wider than the
// pane wraps, \r returns only to the wrapped row, and every frame leaves a
// stale row behind.
func TestSpinnerFrameFitsPane(t *testing.T) {
	withColumns(t, 50)
	frame := spinnerFrame("agent · running · sleep 3; ls -la internal/shell internal/editor internal/agent | head -n 3", "░")
	assertRowsFit(t, frame, 50)
	if !strings.Contains(stripANSI(frame), "…") {
		t.Errorf("truncated frame should end in an ellipsis: %q", frame)
	}
}

func TestHintRowsFitPane(t *testing.T) {
	withColumns(t, 50)
	for _, ct := range []ConfirmationType{ConfirmTypeCommand, ConfirmTypeExplanation, ConfirmTypeError} {
		var buf bytes.Buffer
		aoc := NewAgentOutputCoordinator(&buf)
		aoc.EnterConfirming()
		aoc.ShowHints(ct)
		assertRowsFit(t, buf.String(), 50)
		if !strings.Contains(buf.String(), "enter to") {
			t.Errorf("hint lost its first key: %q", buf.String())
		}
	}
}

// The prompt is erased by a fixed count of five rows: a command that wraps
// added rows the erase never reached.
func TestPermissionPromptFitsPane(t *testing.T) {
	withColumns(t, 50)
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.RenderPermissionPrompt("mkdir -p /tmp/hash-fuzz-permission-test-directory-with-a-long-name", "execute", "")
	assertRowsFit(t, buf.String(), 50)
	if strings.Count(buf.String(), "\r\n") != 5 {
		t.Errorf("prompt should draw five rows, got %q", buf.String())
	}
}

func TestConversationHeaderFitsPane(t *testing.T) {
	withColumns(t, 50)
	frame := agentConversationInputFrame(true)
	assertRowsFit(t, frame.TopLine, 50)
	if !strings.Contains(frame.TopLine, "enter to send") {
		t.Errorf("header lost its first key: %q", frame.TopLine)
	}
}

func TestLearnedFixBannerFitsPane(t *testing.T) {
	withColumns(t, 50)
	var buf bytes.Buffer
	h := &ErrorHandler{out: &buf}
	h.showDidYouMean("git checkout master")
	assertRowsFit(t, buf.String(), 50)
}

func TestToolRowFitsPane(t *testing.T) {
	withColumns(t, 50)
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.RenderToolResult(agent.ToolCallUpdate{
		Kind: "execute", Status: agent.ToolCallCompleted,
		Title: "sleep 3; ls -la internal/shell internal/editor internal/agent | head -n 3",
	}, agentConversationAgentContinuation)
	assertRowsFit(t, buf.String(), 50)
}
