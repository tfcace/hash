package shell

import (
	"strings"
	"testing"
)

func TestStatus_Format(t *testing.T) {
	s := &SystemStatus{
		Version:      "0.3.0",
		PromptMode:   "starship",
		PromptOK:     true,
		HistoryPath:  "~/.local/share/hash/history.db",
		HistoryOK:    true,
		HistoryCount: 1234,
		LearningOK:   true,
		PatternCount: 42,
		AgentName:    "claude",
		AgentOK:      false,
		PTYOK:        true,
		ClipboardOK:  true,
	}

	output := s.Format()

	if !strings.Contains(output, "hash 0.3.0") {
		t.Error("Should contain version")
	}
	if !strings.Contains(output, "starship") {
		t.Error("Should contain prompt mode")
	}
	if !strings.Contains(output, "1,234 entries") {
		t.Error("Should contain formatted history count")
	}
	if !strings.Contains(output, "42 patterns") {
		t.Error("Should contain pattern count")
	}
	if !strings.Contains(output, "not connected") {
		t.Error("Should show agent not connected")
	}
}

func TestStatus_FormatAgentVersionAndUpdate(t *testing.T) {
	s := &SystemStatus{
		Version:      "0.9.0",
		AgentName:    "claude-agent-acp",
		AgentOK:      true,
		AgentVersion: "0.42.0",
		AgentLatest:  "0.88.0",
	}
	out := s.Format()
	if !strings.Contains(out, "claude-agent-acp 0.42.0") {
		t.Errorf("status should show the adapter version:\n%s", out)
	}
	if !strings.Contains(out, "0.88.0 available") || !strings.Contains(out, "model update") {
		t.Errorf("status should offer the update:\n%s", out)
	}

	current := &SystemStatus{AgentName: "claude-agent-acp", AgentOK: true, AgentVersion: "0.88.0"}
	if strings.Contains(current.Format(), "available") {
		t.Error("no update line when current")
	}
}
