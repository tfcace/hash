package shell

import (
	"context"
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
