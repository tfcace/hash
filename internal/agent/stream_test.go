package agent

import (
	"context"
	"testing"
)

func TestStreamCollector_Empty(t *testing.T) {
	c := NewStreamCollector()

	resp := c.Response()
	if resp.Type != ResponseTypeError {
		t.Errorf("expected ResponseTypeError, got %v", resp.Type)
	}
}

func TestStreamCollector_Command(t *testing.T) {
	c := NewStreamCollector()
	c.Append("find . -name ")
	c.Append("'*.go'")

	if c.Text() != "find . -name '*.go'" {
		t.Errorf("expected 'find . -name '*.go'', got %q", c.Text())
	}

	resp := c.Response()
	if resp.Type != ResponseTypeCommand {
		t.Errorf("expected ResponseTypeCommand, got %v", resp.Type)
	}
	if resp.Command != "find . -name '*.go'" {
		t.Errorf("expected command 'find . -name '*.go'', got %q", resp.Command)
	}
}

func TestStreamCollector_Explanation(t *testing.T) {
	c := NewStreamCollector()
	c.Append("This is a long explanation about how to use the find command. ")
	c.Append("It searches for files matching a pattern.")

	resp := c.Response()
	if resp.Type != ResponseTypeExplanation {
		t.Errorf("expected ResponseTypeExplanation, got %v", resp.Type)
	}
}

func TestClient_StreamRequest_Fallback(t *testing.T) {
	// Create a mock transport that doesn't support streaming
	mock := NewMockTransport(Response{
		Type:    ResponseTypeCommand,
		Command: "ls -la",
	})

	client := NewClient(mock)
	ctx := context.Background()

	textCh, errCh := client.StreamRequest(ctx, Request{Prompt: "list files"})

	// Collect text
	var text string
	for chunk := range textCh {
		text += chunk
	}

	// Check for errors
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	default:
	}

	if text != "ls -la" {
		t.Errorf("expected 'ls -la', got %q", text)
	}
}

func TestAgentError(t *testing.T) {
	err := &AgentError{Message: "test error"}
	if err.Error() != "test error" {
		t.Errorf("expected 'test error', got %q", err.Error())
	}
}
