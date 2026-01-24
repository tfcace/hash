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
