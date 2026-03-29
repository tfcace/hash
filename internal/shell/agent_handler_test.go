package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
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

func TestAgentHandler_HandleRequest_NilClient(t *testing.T) {
	handler := NewAgentHandler(nil)
	_, err := handler.HandleRequest(context.Background(), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "test",
	})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
	if !strings.Contains(err.Error(), "no agent configured") {
		t.Errorf("expected 'no agent configured' error, got: %v", err)
	}
}

func TestAgentHandler_BuildRequest_PipeWithEmptyOutput(t *testing.T) {
	mock := agent.NewMockTransport(agent.Response{
		Type:    agent.ResponseTypeCommand,
		Command: "echo done",
	})
	client := agent.NewClient(mock)
	_ = mock.Connect(context.Background())

	handler := NewAgentHandler(client)

	// Set up clipboard with empty output
	buf := clipboard.NewBuffer(10)
	buf.AddCommand("failing-cmd")
	buf.SetOutput("") // empty output
	handler.SetClipboard(buf)

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgentPipe,
		Command:     "failing-cmd",
		AgentPrompt: "explain the error",
	}

	_, err := handler.HandleRequest(context.Background(), parsed)
	if err != nil {
		t.Fatalf("HandleRequest() error = %v", err)
	}

	// Verify the prompt mentions empty/no output
	reqs := mock.Requests()
	if len(reqs) == 0 {
		t.Fatal("expected at least one request")
	}
	prompt := reqs[0].Prompt
	if !strings.Contains(prompt, "no output") && !strings.Contains(prompt, "empty") {
		t.Errorf("pipe with empty output should mention 'no output' or 'empty', got prompt: %q", prompt)
	}
}

func TestAgentHandler_BuildRequest_PipeWithOutput(t *testing.T) {
	mock := agent.NewMockTransport(agent.Response{
		Type:    agent.ResponseTypeCommand,
		Command: "echo done",
	})
	client := agent.NewClient(mock)
	_ = mock.Connect(context.Background())

	handler := NewAgentHandler(client)

	// Set up clipboard with real output
	buf := clipboard.NewBuffer(10)
	buf.AddCommand("ls -la")
	buf.SetOutput("file1.txt\nfile2.txt")
	handler.SetClipboard(buf)

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgentPipe,
		Command:     "ls -la",
		AgentPrompt: "filter text files",
	}

	_, err := handler.HandleRequest(context.Background(), parsed)
	if err != nil {
		t.Fatalf("HandleRequest() error = %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) == 0 {
		t.Fatal("expected at least one request")
	}
	prompt := reqs[0].Prompt
	if !strings.Contains(prompt, "Given the output of") {
		t.Errorf("pipe with output should use 'Given the output of', got prompt: %q", prompt)
	}
	if strings.Contains(prompt, "no output") || strings.Contains(prompt, "empty") {
		t.Errorf("pipe with output should NOT mention 'no output' or 'empty', got prompt: %q", prompt)
	}
}

func TestAgentHandler_BuildRequest_InlineCompletion(t *testing.T) {
	mock := agent.NewMockTransport(agent.Response{
		Type:    agent.ResponseTypeCommand,
		Command: "--format=json",
	})
	client := agent.NewClient(mock)
	_ = mock.Connect(context.Background())

	handler := NewAgentHandler(client)

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgentInline,
		Command:     "kubectl get pods --output=",
		AgentPrompt: "json format",
	}

	_, err := handler.HandleRequest(context.Background(), parsed)
	if err != nil {
		t.Fatalf("HandleRequest() error = %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) == 0 {
		t.Fatal("expected at least one request")
	}
	prompt := reqs[0].Prompt
	if !strings.Contains(prompt, "Complete this shell command argument") {
		t.Errorf("inline request should contain completion instruction, got prompt: %q", prompt)
	}
}

func TestAgentHandler_BuildRequest_InvalidType(t *testing.T) {
	mock := agent.NewMockTransport(agent.Response{})
	client := agent.NewClient(mock)
	_ = mock.Connect(context.Background())

	handler := NewAgentHandler(client)

	parsed := parser.ParseResult{
		Type:    parser.CommandTypeShell,
		Command: "ls -la",
	}

	_, err := handler.HandleRequest(context.Background(), parsed)
	if err == nil {
		t.Fatal("expected error for non-agent request type")
	}
	if !strings.Contains(err.Error(), "not an agent request") {
		t.Errorf("expected 'not an agent request' error, got: %v", err)
	}
}

func TestAgentHandler_StreamRequest_NilClient(t *testing.T) {
	handler := NewAgentHandler(nil)
	textCh, errCh := handler.StreamRequest(context.Background(), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "test",
	})

	// textCh should be nil, errCh should have an error
	if textCh != nil {
		t.Error("expected nil textCh for nil client")
	}
	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "no agent configured") {
		t.Errorf("expected 'no agent configured' error, got: %v", err)
	}
}
