package agent

import (
	"context"
	"testing"
)

func TestAgentRequest_Context(t *testing.T) {
	req := Request{
		Prompt: "find large files",
		Context: Context{
			Cwd:       "/home/user/projects",
			GitBranch: "main",
		},
	}

	if req.Prompt != "find large files" {
		t.Errorf("Prompt = %q, want %q", req.Prompt, "find large files")
	}
	if req.Context.Cwd != "/home/user/projects" {
		t.Errorf("Cwd = %q, want %q", req.Context.Cwd, "/home/user/projects")
	}
}

func TestAgentResponse_Command(t *testing.T) {
	resp := Response{
		Type:    ResponseTypeCommand,
		Command: "find . -size +100M",
	}

	if resp.Type != ResponseTypeCommand {
		t.Errorf("Type = %v, want %v", resp.Type, ResponseTypeCommand)
	}
	if resp.Command != "find . -size +100M" {
		t.Errorf("Command = %q, want %q", resp.Command, "find . -size +100M")
	}
}

func TestAgentResponse_Explanation(t *testing.T) {
	resp := Response{
		Type:        ResponseTypeExplanation,
		Explanation: "This command finds files larger than 100MB",
	}

	if resp.Type != ResponseTypeExplanation {
		t.Errorf("Type = %v, want %v", resp.Type, ResponseTypeExplanation)
	}
}

func TestClient_Ask(t *testing.T) {
	mockResp := Response{
		Type:    ResponseTypeCommand,
		Command: "find . -size +100M",
	}
	mock := NewMockTransport(mockResp)

	client := NewClient(mock)
	ctx := context.Background()

	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	req := Request{
		Prompt: "find large files",
		Context: Context{
			Cwd: "/home/user",
		},
	}

	resp, err := client.Ask(ctx, req)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	if resp.Type != ResponseTypeCommand {
		t.Errorf("Type = %v, want %v", resp.Type, ResponseTypeCommand)
	}
	if resp.Command != "find . -size +100M" {
		t.Errorf("Command = %q, want %q", resp.Command, "find . -size +100M")
	}

	// Verify request was captured
	if len(mock.Requests()) != 1 {
		t.Errorf("Requests count = %d, want 1", len(mock.Requests()))
	}
	if mock.Requests()[0].Prompt != "find large files" {
		t.Errorf("Request prompt = %q, want %q", mock.Requests()[0].Prompt, "find large files")
	}
}
