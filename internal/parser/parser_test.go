package parser

import (
	"testing"
)

func TestParse_RegularCommand(t *testing.T) {
	result := Parse("echo hello")

	if result.Type != CommandTypeRegular {
		t.Errorf("Type = %v, want %v", result.Type, CommandTypeRegular)
	}
	if result.Command != "echo hello" {
		t.Errorf("Command = %q, want %q", result.Command, "echo hello")
	}
}

func TestParse_AgentPrefix(t *testing.T) {
	result := Parse("?? find large files")

	if result.Type != CommandTypeAgent {
		t.Errorf("Type = %v, want %v", result.Type, CommandTypeAgent)
	}
	if result.AgentPrompt != "find large files" {
		t.Errorf("AgentPrompt = %q, want %q", result.AgentPrompt, "find large files")
	}
}

func TestParse_AgentPipe(t *testing.T) {
	result := Parse("kubectl get pods | ?? filter crashed")

	if result.Type != CommandTypeAgentPipe {
		t.Errorf("Type = %v, want %v", result.Type, CommandTypeAgentPipe)
	}
	if result.Command != "kubectl get pods" {
		t.Errorf("Command = %q, want %q", result.Command, "kubectl get pods")
	}
	if result.AgentPrompt != "filter crashed" {
		t.Errorf("AgentPrompt = %q, want %q", result.AgentPrompt, "filter crashed")
	}
}

func TestParse_AgentInline(t *testing.T) {
	result := Parse("kubectl get pods --sort-by=?? restart count")

	if result.Type != CommandTypeAgentInline {
		t.Errorf("Type = %v, want %v", result.Type, CommandTypeAgentInline)
	}
	if result.Command != "kubectl get pods --sort-by=" {
		t.Errorf("Command = %q, want %q", result.Command, "kubectl get pods --sort-by=")
	}
	if result.AgentPrompt != "restart count" {
		t.Errorf("AgentPrompt = %q, want %q", result.AgentPrompt, "restart count")
	}
}

func TestParse_EmptyLine(t *testing.T) {
	result := Parse("")

	if result.Type != CommandTypeEmpty {
		t.Errorf("Type = %v, want %v", result.Type, CommandTypeEmpty)
	}
}

func TestParse_JustQuestionMarks(t *testing.T) {
	result := Parse("??")

	if result.Type != CommandTypeAgent {
		t.Errorf("Type = %v, want %v", result.Type, CommandTypeAgent)
	}
	if result.AgentPrompt != "" {
		t.Errorf("AgentPrompt = %q, want empty", result.AgentPrompt)
	}
}
