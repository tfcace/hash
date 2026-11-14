package shell

import (
	"bytes"
	"strings"
	"testing"
)

func TestResponseUI_ShowAgentHintIncludesACPInstallCommand(t *testing.T) {
	var buf bytes.Buffer
	ui := NewResponseUI(&buf)

	ui.ShowAgentHint("stdio", "claude-agent-acp", "")
	output := buf.String()

	if !strings.Contains(output, "npm install -g @agentclientprotocol/claude-agent-acp") {
		t.Fatalf("expected claude-agent-acp install hint, got:\n%s", output)
	}
	if !strings.Contains(output, "which claude-agent-acp") {
		t.Fatalf("expected PATH hint for claude-agent-acp, got:\n%s", output)
	}
}

func TestAgentStateStringUsesScopedConversationStyle(t *testing.T) {
	got := AgentStateThinking.String()
	if !bytes.Contains([]byte(got), []byte("agent ·")) {
		t.Fatalf("thinking state should include scoped agent label, got %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("thinking")) {
		t.Fatalf("thinking state should use lower-case status copy, got %q", got)
	}
}

func TestAgentStatusMotionUsesReplacementGlyphs(t *testing.T) {
	motion := selectAgentStatusMotion(0, &scriptedIntn{values: []int{0}})
	line := formatAgentStatusMotion("agent · thinking", motion)
	if !bytes.Contains([]byte(line), []byte("agent · thinking")) {
		t.Fatalf("status line should include state text, got %q", line)
	}
	if !strings.HasPrefix(line, " \033[") {
		t.Fatalf("status line should be indented by one space, got %q", line)
	}
	for _, motion := range agentStatusReplacementPool {
		if got := len([]rune(motion)); got != 1 {
			t.Fatalf("status motion frame should stay compact, frame %q has width %d", motion, got)
		}
	}
	for _, want := range []string{"h", "a", "s", "/"} {
		if !containsString(agentStatusReplacementPool, want) {
			t.Fatalf("status replacement pool should include %q", want)
		}
	}
	for _, want := range []string{"-", "\\", "|", "+", "*"} {
		if !containsString(agentStatusReplacementPool, want) {
			t.Fatalf("status replacement pool should include post-style glyph %q", want)
		}
	}
	for _, want := range []string{"░", "▒", "▓", "█"} {
		if !containsString(agentStatusReplacementPool, want) {
			t.Fatalf("status replacement pool should include post block glyph %q", want)
		}
	}
	for _, quadrant := range []string{"▖", "▘", "▝", "▗"} {
		if containsString(agentStatusReplacementPool, quadrant) {
			t.Fatalf("status replacement pool should avoid quadrant glyph %q", quadrant)
		}
	}
	if bytes.Contains([]byte(line), []byte("⠋")) {
		t.Fatalf("status motion should not use braille spinner frames, got %q", line)
	}
}

func TestAgentStatusMotionRandomlySelectsFromReplacementPool(t *testing.T) {
	motion := selectAgentStatusMotion(0, &scriptedIntn{values: []int{indexString(agentStatusReplacementPool, "/")}})
	if motion != "/" {
		t.Fatalf("status motion should use random glyphs from the replacement pool, got %q", motion)
	}

	motion = selectAgentStatusMotion(0, &scriptedIntn{values: []int{indexString(agentStatusReplacementPool, "s")}})
	if motion != "s" {
		t.Fatalf("status motion should use random letters from hash in the replacement pool, got %q", motion)
	}
	line := formatAgentStatusMotion("bot · ok", motion)
	if !bytes.Contains([]byte(line), []byte("s")) {
		t.Fatalf("status line should render the selected hash letter, got %q", line)
	}
}

func TestAgentStatusMotionGlyphUsesLiveRailColor(t *testing.T) {
	line := formatAgentStatusMotion("agent · thinking", "█")
	wantGlyph := agentConversationLiveRailStyle + "█\033[0m"
	if !strings.Contains(line, wantGlyph) {
		t.Fatalf("status motion glyph should use live rail color, got %q", line)
	}
	wantText := "\033[90magent · thinking\033[0m"
	if !strings.Contains(line, wantText) {
		t.Fatalf("status text should stay dim, got %q", line)
	}
	if strings.Contains(line, agentConversationLiveRailStyle+"agent · thinking") {
		t.Fatalf("live rail color should apply only to status glyph, got %q", line)
	}
}

func TestAgentStatusMotionAnimatesFrames(t *testing.T) {
	first := formatAgentStatusMotion("agent · thinking", selectAgentStatusMotion(0, nil))
	second := formatAgentStatusMotion("agent · thinking", selectAgentStatusMotion(1, nil))
	if first == second {
		t.Fatalf("status motion should change across frames, got %q", first)
	}
}

func containsString(values []string, want string) bool {
	return indexString(values, want) >= 0
}

func indexString(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

type scriptedIntn struct {
	values []int
}

func (s *scriptedIntn) Intn(n int) int {
	if s == nil || len(s.values) == 0 {
		return 0
	}
	value := s.values[0]
	s.values = s.values[1:]
	return value % n
}

// An agent's error message is drawn inert: an escape sequence in it shows
// as text instead of driving the terminal (clearing the screen, setting
// the title). Its lines stay lines.
func TestResponseUI_ShowErrorDrawsTheMessageInert(t *testing.T) {
	var buf bytes.Buffer
	NewResponseUI(&buf).ShowError("rpc failed\x1b[2J\x1b]0;pwned\x07\nsecond line")
	got := buf.String()
	if want := "\033[31m✗ rpc failed^[[2J^[]0;pwned^G\nsecond line\033[0m\n"; got != want {
		t.Errorf("ShowError wrote %q, want %q", got, want)
	}
}
