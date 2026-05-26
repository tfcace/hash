package agent

import (
	"context"
	"sync"
	"testing"
)

// TestClient_SequentialAsk tests sequential Ask calls on the same client.
// Note: The real ACPTransport is designed for single-threaded use per session
// (one request at a time). MockTransport similarly doesn't support concurrent SendStreaming.
// This test verifies basic sequential usage.
func TestClient_SequentialAsk(t *testing.T) {
	mock := NewMockTransport(Response{
		Type:    ResponseTypeCommand,
		Command: "ls -la",
	})
	client := NewClient(mock)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	// Sequential requests
	for i := 0; i < 10; i++ {
		req := Request{
			Prompt: "list files",
			Context: Context{
				Cwd: "/home/user",
			},
		}
		resp, err := client.Ask(ctx, req)
		if err != nil {
			t.Errorf("Ask() error = %v", err)
		}
		if resp.Command != "ls -la" {
			t.Errorf("Ask() command = %q, want %q", resp.Command, "ls -la")
		}
	}
}

// TestClient_SequentialStreamRequest tests sequential StreamRequest calls.
func TestClient_SequentialStreamRequest(t *testing.T) {
	mock := NewMockTransport(Response{
		Type:    ResponseTypeCommand,
		Command: "echo hello",
	})
	client := NewClient(mock)

	ctx := context.Background()

	// Sequential requests
	for i := 0; i < 10; i++ {
		textCh, errCh := client.StreamRequest(ctx, Request{Prompt: "test"})

		// Drain channels
		var text string
		for chunk := range textCh {
			text += chunk
		}
		for err := range errCh {
			if err != nil {
				t.Errorf("StreamRequest() error = %v", err)
			}
		}
		if text != "echo hello" {
			t.Errorf("StreamRequest() text = %q, want %q", text, "echo hello")
		}
	}
}

// TestStreamCollector_SequentialAppend tests sequential Append calls.
// Note: StreamCollector uses strings.Builder which is not thread-safe by design.
// This is fine because StreamCollector is only used within a single streaming goroutine.
func TestStreamCollector_SequentialAppend(t *testing.T) {
	c := NewStreamCollector()

	// Sequential appends
	for i := 0; i < 100; i++ {
		c.Append("x")
	}

	text := c.Text()
	if len(text) != 100 {
		t.Errorf("expected 100 chars, got %d", len(text))
	}
}

// TestMockTransport_SequentialSendStreaming tests sequential SendStreaming operations.
// Note: MockTransport is a test helper not designed for concurrent SendStreaming - that's
// expected. This test verifies basic sequential usage.
func TestMockTransport_SequentialSendStreaming(t *testing.T) {
	mock := NewMockTransport(Response{
		Type:    ResponseTypeCommand,
		Command: "test",
	})

	ctx := context.Background()
	const numRequests = 10

	for i := 0; i < numRequests; i++ {
		textCh, errCh := mock.SendStreaming(ctx, Request{Prompt: "test"})
		// Drain channels
		for range textCh {
		}
		for range errCh {
		}
	}

	// All requests should be recorded
	requests := mock.Requests()
	if len(requests) != numRequests {
		t.Errorf("expected %d requests, got %d", numRequests, len(requests))
	}
}

// TestMockTransport_SequentialConnectClose tests sequential Connect/Close operations.
// Note: MockTransport is not designed for concurrent Connect/Close - that's expected
// for a test mock. This test verifies basic sequential usage.
func TestMockTransport_SequentialConnectClose(t *testing.T) {
	mock := NewMockTransport(Response{
		Type:    ResponseTypeCommand,
		Command: "test",
	})

	ctx := context.Background()

	// Sequential connect/close cycles
	for i := 0; i < 10; i++ {
		if err := mock.Connect(ctx); err != nil {
			t.Errorf("Connect() error = %v", err)
		}
		if err := mock.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}
}

// TestParseAgentResponse_Concurrent tests concurrent parseAgentResponse calls.
// Run with: go test -race -run TestParseAgentResponse_Concurrent ./internal/agent/...
func TestParseAgentResponse_Concurrent(t *testing.T) {
	inputs := []string{
		"ls -la",
		"find . -name '*.go'",
		"This is an explanation.",
		"git push origin main",
		"",
	}

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines * len(inputs))

	for i := 0; i < numGoroutines; i++ {
		for _, input := range inputs {
			go func(in string) {
				defer wg.Done()
				_ = parseAgentResponse(in)
			}(input)
		}
	}

	wg.Wait()
}

// TestLooksLikeCommand_Concurrent tests concurrent looksLikeCommand calls.
// Run with: go test -race -run TestLooksLikeCommand_Concurrent ./internal/agent/...
func TestLooksLikeCommand_Concurrent(t *testing.T) {
	inputs := []string{
		"ls -la",
		"This is an explanation.",
		"find . -name '*.go'",
		"",
		"The answer is 42",
	}

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines * len(inputs))

	for i := 0; i < numGoroutines; i++ {
		for _, input := range inputs {
			go func(in string) {
				defer wg.Done()
				_ = looksLikeCommand(in)
			}(input)
		}
	}

	wg.Wait()
}

// TestBuildPromptWithContext_Concurrent tests concurrent buildPromptWithContext calls.
// Run with: go test -race -run TestBuildPromptWithContext_Concurrent ./internal/agent/...
func TestBuildPromptWithContext_Concurrent(t *testing.T) {
	req := Request{
		Prompt: "find large files",
		Context: Context{
			Cwd:         "/home/user",
			GitBranch:   "main",
			KubeContext: "production",
			History:     []string{"ls", "cd /tmp"},
			EnvVars:     map[string]string{"PATH": "/usr/bin"},
			LastOutput:  "output",
			LastError:   "error",
		},
	}

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_ = buildPromptWithContext(req)
		}()
	}

	wg.Wait()
}
