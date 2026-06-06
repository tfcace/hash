//go:build e2e_live

// These tests run against the real claude-agent-acp agent.
// They are non-deterministic and may fail due to agent response variations.
// Run with: go test -tags=e2e_live -v ./e2e/...

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/shell"
)

// testTimeout is the maximum time for each test
const testTimeout = 60 * time.Second

// newLiveAgent creates a real ACP transport connected to claude-agent-acp.
func newLiveAgent(t *testing.T) (*agent.ACPTransport, func()) {
	t.Helper()

	transport := agent.NewACPTransport(agent.ACPConfig{
		Command: "claude-agent-acp",
		Args:    []string{},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect to claude-agent-acp: %v", err)
	}

	cleanup := func() {
		transport.Close()
	}

	return transport, cleanup
}

// TestLive_FullCommand tests "?? <prompt>" with real agent.
func TestLive_FullCommand(t *testing.T) {
	transport, cleanup := newLiveAgent(t)
	defer cleanup()

	client := agent.NewClient(transport)
	handler := shell.NewAgentHandler(client)

	tests := []struct {
		name        string
		input       string
		wantType    agent.ResponseType
		shouldMatch func(string) bool
	}{
		{
			name:     "find go files",
			input:    "?? find all Go files in current directory",
			wantType: agent.ResponseTypeCommand,
			shouldMatch: func(cmd string) bool {
				// Should contain find command or similar
				return strings.Contains(cmd, "find") ||
					strings.Contains(cmd, "*.go") ||
					strings.Contains(cmd, "go") ||
					strings.Contains(cmd, "ls")
			},
		},
		{
			name:     "list files",
			input:    "?? list files sorted by size",
			wantType: agent.ResponseTypeCommand,
			shouldMatch: func(cmd string) bool {
				return strings.Contains(cmd, "ls") ||
					strings.Contains(cmd, "find") ||
					strings.Contains(cmd, "sort")
			},
		},
		{
			name:     "disk usage",
			input:    "?? show disk usage of current directory",
			wantType: agent.ResponseTypeCommand,
			shouldMatch: func(cmd string) bool {
				return strings.Contains(cmd, "du") ||
					strings.Contains(cmd, "df") ||
					strings.Contains(cmd, "disk")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each subtest gets its own timeout context
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgent {
				t.Fatalf("Parse type = %v, want Agent", parsed.Type)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest error: %v", err)
			}

			t.Logf("Response type: %v", resp.Type)
			t.Logf("Command: %q", resp.Command)
			t.Logf("Explanation: %q", resp.Explanation)

			if resp.Type == agent.ResponseTypeError {
				t.Fatalf("Got error response: %s", resp.Error)
			}

			// Check if response contains expected elements
			responseText := resp.Command
			if responseText == "" {
				responseText = resp.Explanation
			}

			if responseText == "" {
				t.Error("Got empty response")
			}

			if tt.shouldMatch != nil && !tt.shouldMatch(responseText) {
				t.Logf("Response may not match expected pattern (non-fatal): %q", responseText)
			}
		})
	}
}

// TestLive_PipeCommand tests "cmd | ?? <prompt>" with real agent.
// NOTE: Ambiguous prompts like "convert to json" may cause the agent to DO the work
// rather than SUGGEST a command. Use explicit prompts like "what command..." for reliable results.
func TestLive_PipeCommand(t *testing.T) {
	transport, cleanup := newLiveAgent(t)
	defer cleanup()

	client := agent.NewClient(transport)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Simulate having CSV data from a previous command
	csvData := `name,age,city
Alice,30,New York
Bob,25,San Francisco
Charlie,35,Chicago`

	clipBuf.AddCommand("cat data.csv")
	clipBuf.SetOutput(csvData)

	// Pipe test - agent does the work (converts data)
	parsed := parser.Parse("cat data.csv | ?? convert to json")
	if parsed.Type != parser.CommandTypeAgentPipe {
		t.Fatalf("Parse type = %v, want AgentPipe", parsed.Type)
	}

	resp, err := handler.HandleRequest(ctx, parsed)
	if err != nil {
		t.Fatalf("HandleRequest error: %v", err)
	}

	t.Logf("Response type: %v", resp.Type)
	t.Logf("Command: %q", resp.Command)
	t.Logf("Explanation: %q", resp.Explanation)

	if resp.Type == agent.ResponseTypeError {
		t.Fatalf("Got error response: %s", resp.Error)
	}

	responseText := resp.Command
	if responseText == "" {
		responseText = resp.Explanation
	}

	if responseText == "" {
		t.Error("Got empty response")
	}

	// The response should mention JSON transformation
	if !strings.Contains(strings.ToLower(responseText), "json") &&
		!strings.Contains(responseText, "jq") &&
		!strings.Contains(responseText, "python") {
		t.Logf("Response may not be JSON-related (non-fatal): %q", responseText)
	}
}

// TestLive_InlineCommand tests "--flag=?? <prompt>" with real agent.
func TestLive_InlineCommand(t *testing.T) {
	transport, cleanup := newLiveAgent(t)
	defer cleanup()

	client := agent.NewClient(transport)
	handler := shell.NewAgentHandler(client)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	tests := []struct {
		name   string
		input  string
		prefix string
	}{
		{
			name:   "git log format",
			input:  "git log --format=?? one line with hash and message",
			prefix: "git log --format=",
		},
		{
			name:   "find type equals",
			input:  "find . -type=?? regular files",
			prefix: "find . -type=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgentInline {
				t.Fatalf("Parse type = %v, want AgentInline", parsed.Type)
			}

			if parsed.Command != tt.prefix {
				t.Fatalf("Parsed prefix = %q, want %q", parsed.Command, tt.prefix)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest error: %v", err)
			}

			t.Logf("Response type: %v", resp.Type)
			t.Logf("Command (fill): %q", resp.Command)
			t.Logf("Complete: %q", tt.prefix+resp.Command)

			if resp.Type == agent.ResponseTypeError {
				t.Fatalf("Got error response: %s", resp.Error)
			}

			// For inline, we expect just the fill portion
			if resp.Command == "" && resp.Explanation == "" {
				t.Error("Got empty response")
			}
		})
	}
}

// TestLive_GitDiff tests conventional commit generation.
func TestLive_GitDiff(t *testing.T) {
	transport, cleanup := newLiveAgent(t)
	defer cleanup()

	client := agent.NewClient(transport)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Simulate git diff output
	diffOutput := `diff --git a/main.go b/main.go
index abc1234..def5678 100644
--- a/main.go
+++ b/main.go
@@ -10,3 +10,7 @@ func main() {
+	// Add logging
+	log.Println("Starting application")
+
 	server.Start()
 }`

	clipBuf.AddCommand("git diff --staged")
	clipBuf.SetOutput(diffOutput)

	parsed := parser.Parse("git diff --staged | ?? generate conventional commit message")
	if parsed.Type != parser.CommandTypeAgentPipe {
		t.Fatalf("Parse type = %v, want AgentPipe", parsed.Type)
	}

	resp, err := handler.HandleRequest(ctx, parsed)
	if err != nil {
		t.Fatalf("HandleRequest error: %v", err)
	}

	t.Logf("Response type: %v", resp.Type)
	t.Logf("Command: %q", resp.Command)
	t.Logf("Explanation: %q", resp.Explanation)

	if resp.Type == agent.ResponseTypeError {
		t.Fatalf("Got error response: %s", resp.Error)
	}

	responseText := resp.Command
	if responseText == "" {
		responseText = resp.Explanation
	}

	// Should contain git commit or a commit message pattern
	if !strings.Contains(responseText, "git commit") &&
		!strings.Contains(responseText, "feat") &&
		!strings.Contains(responseText, "fix") &&
		!strings.Contains(responseText, "chore") {
		t.Logf("Response may not be conventional commit (non-fatal): %q", responseText)
	}
}

// TestLive_KubectlPods tests kubernetes pod filtering.
func TestLive_KubectlPods(t *testing.T) {
	transport, cleanup := newLiveAgent(t)
	defer cleanup()

	client := agent.NewClient(transport)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Simulate kubectl output
	kubectlOutput := `NAMESPACE     NAME                                READY   STATUS             RESTARTS   AGE
default       nginx-deployment-5d59c5d84-abc12   1/1     Running            0          5d
kube-system   coredns-5dd5756b68-xyz99          0/1     CrashLoopBackOff   15         2d`

	clipBuf.AddCommand("kubectl get pods -A")
	clipBuf.SetOutput(kubectlOutput)

	// Pipe test - agent filters the data
	parsed := parser.Parse("kubectl get pods -A | ?? show only unhealthy pods")
	if parsed.Type != parser.CommandTypeAgentPipe {
		t.Fatalf("Parse type = %v, want AgentPipe", parsed.Type)
	}

	resp, err := handler.HandleRequest(ctx, parsed)
	if err != nil {
		t.Fatalf("HandleRequest error: %v", err)
	}

	t.Logf("Response type: %v", resp.Type)
	t.Logf("Command: %q", resp.Command)
	t.Logf("Explanation: %.200s...", resp.Explanation)

	if resp.Type == agent.ResponseTypeError {
		t.Fatalf("Got error response: %s", resp.Error)
	}

	// Should contain filtering command
	responseText := resp.Command
	if responseText == "" {
		responseText = resp.Explanation
	}

	if responseText == "" {
		t.Error("Got empty response")
	}
}

// TestLive_ContextPropagation tests that context is properly sent to agent.
func TestLive_ContextPropagation(t *testing.T) {
	transport, cleanup := newLiveAgent(t)
	defer cleanup()

	client := agent.NewClient(transport)
	handler := shell.NewAgentHandler(client)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Get current directory for context
	cwd, _ := os.Getwd()

	parsed := parser.Parse("?? what directory am I in")

	resp, err := handler.HandleRequest(ctx, parsed)
	if err != nil {
		t.Fatalf("HandleRequest error: %v", err)
	}

	t.Logf("Current directory: %s", cwd)
	t.Logf("Response: %q", resp.Command)
	t.Logf("Explanation: %q", resp.Explanation)

	// The response should reference the current directory or pwd command
	responseText := resp.Command
	if responseText == "" {
		responseText = resp.Explanation
	}

	if !strings.Contains(responseText, "pwd") &&
		!strings.Contains(responseText, cwd) &&
		!strings.Contains(strings.ToLower(responseText), "directory") {
		t.Logf("Response may not reference directory context (non-fatal): %q", responseText)
	}
}

// TestLive_Timeout tests that timeouts are handled gracefully.
func TestLive_Timeout(t *testing.T) {
	transport, cleanup := newLiveAgent(t)
	defer cleanup()

	client := agent.NewClient(transport)
	handler := shell.NewAgentHandler(client)

	// Very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	parsed := parser.Parse("?? explain quantum computing in detail")

	_, err := handler.HandleRequest(ctx, parsed)

	// We expect either a timeout or a very fast response
	if err != nil {
		if strings.Contains(err.Error(), "deadline") || strings.Contains(err.Error(), "timeout") {
			t.Log("Timeout handled correctly")
		} else {
			t.Logf("Got error (may be expected): %v", err)
		}
	} else {
		t.Log("Got response before timeout (fast agent)")
	}
}

// TestLive_MultipleRequests tests session reuse across requests.
func TestLive_MultipleRequests(t *testing.T) {
	transport, cleanup := newLiveAgent(t)
	defer cleanup()

	client := agent.NewClient(transport)
	handler := shell.NewAgentHandler(client)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout*2)
	defer cancel()

	// Send multiple requests to same session
	prompts := []string{
		"?? list files",
		"?? current directory",
		"?? date command",
	}

	for i, input := range prompts {
		t.Run(input, func(t *testing.T) {
			parsed := parser.Parse(input)

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("Request %d error: %v", i, err)
			}

			if resp.Type == agent.ResponseTypeError {
				t.Fatalf("Request %d got error: %s", i, resp.Error)
			}

			t.Logf("Request %d response: %q", i, resp.Command)
		})
	}
}
