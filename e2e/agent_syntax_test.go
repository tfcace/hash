//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/shell"
)

// TestAgentSyntax_FullCommand tests the "?? <prompt>" syntax.
// Website promise: "?? find all Go files modified today" → "find . -name "*.go" -mtime 0"
func TestAgentSyntax_FullCommand(t *testing.T) {
	mock := NewScenarioMock().
		OnPromptContains("large files", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `find . -size +100M`,
		}).
		OnPromptContains("Go files", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `find . -name "*.go" -mtime 0`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name        string
		input       string
		wantCommand string
	}{
		{
			name:        "find go files",
			input:       "?? find all Go files modified today",
			wantCommand: `find . -name "*.go" -mtime 0`,
		},
		{
			name:        "find large files",
			input:       "?? find large files",
			wantCommand: `find . -size +100M`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgent {
				t.Fatalf("Parse type = %v, want %v", parsed.Type, parser.CommandTypeAgent)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if resp.Command != tt.wantCommand {
				t.Errorf("Command = %q, want %q", resp.Command, tt.wantCommand)
			}
		})
	}
}

// TestAgentSyntax_Pipe tests the "cmd | ?? <prompt>" syntax.
// Website promise: "kubectl get pods -o json | ?? extract names and status"
func TestAgentSyntax_Pipe(t *testing.T) {
	mock := NewScenarioMock().
		OnPipePromptContains("extract names", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `jq -r '.items[] | "\(.metadata.name) \(.status.phase)"'`,
		}).
		OnPipePromptContains("convert to json", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `python3 -c "import csv,json,sys; print(json.dumps(list(csv.DictReader(sys.stdin))))"`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	// Set up clipboard buffer to simulate pipe output
	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name        string
		input       string
		pipeOutput  string
		wantCommand string
	}{
		{
			name:        "kubectl extract pods",
			input:       "kubectl get pods -o json | ?? extract names and status",
			pipeOutput:  `{"items":[{"metadata":{"name":"pod1"},"status":{"phase":"Running"}}]}`,
			wantCommand: `jq -r '.items[] | "\(.metadata.name) \(.status.phase)"'`,
		},
		{
			name:        "csv to json",
			input:       "cat data.csv | ?? convert to json array of objects",
			pipeOutput:  "name,age\nAlice,30\nBob,25",
			wantCommand: `python3 -c "import csv,json,sys; print(json.dumps(list(csv.DictReader(sys.stdin))))"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgentPipe {
				t.Fatalf("Parse type = %v, want %v", parsed.Type, parser.CommandTypeAgentPipe)
			}

			// Simulate pipe output: add command first, then set its output
			clipBuf.AddCommand(parsed.Command)
			clipBuf.SetOutput(tt.pipeOutput)

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if resp.Command != tt.wantCommand {
				t.Errorf("Command = %q, want %q", resp.Command, tt.wantCommand)
			}

			// Verify the mock received the pipe output in context
			lastReq, ok := mock.LastRequest()
			if !ok {
				t.Fatal("No request captured")
			}
			if lastReq.Context.LastOutput != tt.pipeOutput {
				t.Errorf("Request.Context.LastOutput = %q, want %q", lastReq.Context.LastOutput, tt.pipeOutput)
			}
		})
	}
}

// TestAgentSyntax_Inline tests the "--flag=?? <prompt>" syntax.
// Website promise: "git log --format=?? oneline with hash" → "git log --format="%h %s""
func TestAgentSyntax_Inline(t *testing.T) {
	mock := NewScenarioMock().
		OnInlineContains("--format=", "oneline", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `%h %s`,
		}).
		OnInlineContains("--sort-by=", "restart", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `.status.containerStatuses[0].restartCount`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name         string
		input        string
		wantPartial  string
		wantFill     string
		wantComplete string
	}{
		{
			name:         "git log format",
			input:        "git log --format=?? oneline with hash",
			wantPartial:  "git log --format=",
			wantFill:     `%h %s`,
			wantComplete: `git log --format=%h %s`,
		},
		{
			name:         "kubectl sort-by",
			input:        "kubectl get pods --sort-by=?? restart count",
			wantPartial:  "kubectl get pods --sort-by=",
			wantFill:     `.status.containerStatuses[0].restartCount`,
			wantComplete: `kubectl get pods --sort-by=.status.containerStatuses[0].restartCount`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgentInline {
				t.Fatalf("Parse type = %v, want %v", parsed.Type, parser.CommandTypeAgentInline)
			}

			if parsed.Command != tt.wantPartial {
				t.Errorf("Partial command = %q, want %q", parsed.Command, tt.wantPartial)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if resp.Command != tt.wantFill {
				t.Errorf("Fill = %q, want %q", resp.Command, tt.wantFill)
			}

			// Verify complete command assembly
			complete := parsed.Command + resp.Command
			if complete != tt.wantComplete {
				t.Errorf("Complete = %q, want %q", complete, tt.wantComplete)
			}
		})
	}
}

// TestAgentSyntax_WithChaos tests agent resilience to failures.
func TestAgentSyntax_WithChaos(t *testing.T) {
	// Use deterministic seed for reproducible tests
	mock := NewScenarioMockWithSeed(12345).
		OnPromptContains("find", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `find . -name "*.go"`,
		}).
		WithChaos(ChaosConfig{
			FailureRate:    0.3, // 30% chance of failure
			MinDelay:       10 * time.Millisecond,
			MaxDelay:       50 * time.Millisecond,
			TimeoutRate:    0.1, // 10% chance of timeout
			ErrorMessages:  DefaultChaosErrors,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	parsed := parser.Parse("?? find go files")

	// Run multiple times to exercise chaos
	successCount := 0
	errorCount := 0
	iterations := 20

	for i := 0; i < iterations; i++ {
		resp, err := handler.HandleRequest(ctx, parsed)
		if err != nil {
			errorCount++
			continue
		}
		if resp.Type == agent.ResponseTypeError {
			errorCount++
			continue
		}
		successCount++
	}

	t.Logf("Chaos test: %d/%d succeeded, %d errors", successCount, iterations, errorCount)

	// With 30% failure + 10% timeout, we expect roughly 60% success rate
	// Allow for randomness, but ensure we got some of each
	if successCount == 0 {
		t.Error("Expected at least some successful requests")
	}
	if errorCount == 0 {
		t.Error("Expected at least some error responses from chaos injection")
	}
}

// TestAgentSyntax_ContextTimeout tests behavior when context times out.
func TestAgentSyntax_ContextTimeout(t *testing.T) {
	mock := NewScenarioMock().
		OnPromptContains("slow", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `echo slow`,
		}).
		WithDelay(500 * time.Millisecond) // Slow response

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	// Create context that times out before response
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	parsed := parser.Parse("?? slow operation")

	_, err := handler.HandleRequest(ctx, parsed)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}
