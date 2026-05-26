package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/parser"
)

func TestAgentHandler_HandleRequest(t *testing.T) {
	mockResp := agent.Response{
		Type:    agent.ResponseTypeCommand,
		Command: "find . -size +100M",
	}
	mock := agent.NewMockTransport(mockResp)
	client := agent.NewClient(mock)

	handler := NewAgentHandler(client)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	parseResult := parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "find large files",
	}

	resp, err := handler.HandleRequest(ctx, parseResult)
	if err != nil {
		t.Fatalf("HandleRequest() error = %v", err)
	}

	if resp.Command != "find . -size +100M" {
		t.Errorf("Command = %q, want %q", resp.Command, "find . -size +100M")
	}
}

func TestBuildRequest_BareAgentAfterError(t *testing.T) {
	handler := &AgentHandler{}
	handler.SetLastError(&LastError{
		Command:  "hash-upgrrade",
		Stderr:   "hash-upgrrade: command not found",
		ExitCode: 127,
	})

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "", // bare ??
	}

	req, err := handler.buildRequest(parsed)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}

	// Prompt should contain the failed command and error
	if !strings.Contains(req.Prompt, "hash-upgrrade") {
		t.Errorf("prompt should contain failed command, got %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "command not found") {
		t.Errorf("prompt should contain stderr, got %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "127") {
		t.Errorf("prompt should contain exit code, got %q", req.Prompt)
	}

	// Context.LastError should also be populated
	if !strings.Contains(req.Context.LastError, "hash-upgrrade") {
		t.Errorf("Context.LastError should contain failed command, got %q", req.Context.LastError)
	}
}

func TestBuildRequest_BareAgentNoError(t *testing.T) {
	handler := &AgentHandler{}
	// No last error set (last command succeeded)

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "", // bare ??
	}

	req, err := handler.buildRequest(parsed)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}

	// Bare ?? with no error: prompt stays empty
	if req.Prompt != "" {
		t.Errorf("prompt should be empty when no error, got %q", req.Prompt)
	}
	if req.Context.LastError != "" {
		t.Errorf("Context.LastError should be empty, got %q", req.Context.LastError)
	}
}

func TestBuildRequest_AgentWithPromptAfterError(t *testing.T) {
	handler := &AgentHandler{}
	handler.SetLastError(&LastError{
		Command:  "make build",
		Stderr:   "undefined reference to main",
		ExitCode: 2,
	})

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "how do I fix this",
	}

	req, err := handler.buildRequest(parsed)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}

	// Explicit prompt should be used as-is, not replaced
	if req.Prompt != "how do I fix this" {
		t.Errorf("prompt should be user's prompt, got %q", req.Prompt)
	}

	// But Context.LastError should still be populated
	if !strings.Contains(req.Context.LastError, "make build") {
		t.Errorf("Context.LastError should contain failed command, got %q", req.Context.LastError)
	}
	if !strings.Contains(req.Context.LastError, "undefined reference") {
		t.Errorf("Context.LastError should contain stderr, got %q", req.Context.LastError)
	}
}

func TestBuildRequest_LastErrorCleared(t *testing.T) {
	handler := &AgentHandler{}

	// Set error, then clear it
	handler.SetLastError(&LastError{
		Command:  "bad-cmd",
		Stderr:   "not found",
		ExitCode: 127,
	})
	handler.SetLastError(nil)

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "",
	}

	req, err := handler.buildRequest(parsed)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}

	if req.Prompt != "" {
		t.Errorf("prompt should be empty after error cleared, got %q", req.Prompt)
	}
	if req.Context.LastError != "" {
		t.Errorf("Context.LastError should be empty after cleared, got %q", req.Context.LastError)
	}
}

func TestBuildRequest_PipeModeIgnoresLastError(t *testing.T) {
	handler := &AgentHandler{}
	handler.SetLastError(&LastError{
		Command:  "bad-cmd",
		Stderr:   "not found",
		ExitCode: 127,
	})

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgentPipe,
		Command:     "ls -la",
		AgentPrompt: "filter hidden files",
	}

	req, err := handler.buildRequest(parsed)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}

	// Pipe mode uses its own prompt format, not the error context
	if !strings.Contains(req.Prompt, "ls -la") {
		t.Errorf("pipe prompt should contain command, got %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "filter hidden files") {
		t.Errorf("pipe prompt should contain user prompt, got %q", req.Prompt)
	}

	// But Context.LastError is still available for the agent
	if !strings.Contains(req.Context.LastError, "bad-cmd") {
		t.Errorf("Context.LastError should still be set, got %q", req.Context.LastError)
	}
}
