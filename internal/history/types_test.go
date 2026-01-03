package history

import (
	"testing"
	"time"
)

func TestCommand_Fields(t *testing.T) {
	cmd := Command{
		ID:         1,
		Command:    "kubectl get pods",
		Cwd:        "/home/user/project",
		ExitCode:   0,
		DurationMs: 150,
		Timestamp:  time.Now(),
		GitBranch:  "main",
		IsSudo:     false,
	}

	if cmd.Command != "kubectl get pods" {
		t.Errorf("Command = %q, want %q", cmd.Command, "kubectl get pods")
	}
	if cmd.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", cmd.ExitCode)
	}
}

func TestCommand_SudoTracking(t *testing.T) {
	cmd := Command{
		Command:    "apt-get update",
		IsSudo:     true,
		SudoUser:   "root",
		RawCommand: "sudo apt-get update",
	}

	if !cmd.IsSudo {
		t.Error("IsSudo should be true")
	}
	if cmd.SudoUser != "root" {
		t.Errorf("SudoUser = %q, want %q", cmd.SudoUser, "root")
	}
	if cmd.RawCommand != "sudo apt-get update" {
		t.Errorf("RawCommand = %q, want %q", cmd.RawCommand, "sudo apt-get update")
	}
}

func TestAgentInteraction_Fields(t *testing.T) {
	interaction := AgentInteraction{
		ID:        1,
		Prompt:    "find large files",
		Response:  "find . -size +100M",
		Accepted:  true,
		CommandID: 5,
		Agent:     "claude",
		LatencyMs: 1200,
		Timestamp: time.Now(),
	}

	if interaction.Prompt != "find large files" {
		t.Errorf("Prompt = %q, want %q", interaction.Prompt, "find large files")
	}
	if !interaction.Accepted {
		t.Error("Accepted should be true")
	}
}
