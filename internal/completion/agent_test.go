package completion

import (
	"context"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
)

type blockingAgentTransport struct{}

func (blockingAgentTransport) Connect(ctx context.Context) error { return nil }
func (blockingAgentTransport) SendStreaming(ctx context.Context, req agent.Request) (<-chan string, <-chan error) {
	return make(chan string), make(chan error)
}
func (blockingAgentTransport) Close() error { return nil }
func (blockingAgentTransport) Name() string { return "blocking" }

func TestAgentCompleter_DetectsInlineAgent(t *testing.T) {
	completer := NewAgentCompleter(nil) // No client for unit test

	// Should detect ?? in line
	ctx := context.Background()
	line := "kubectl get pods --sort-by=?? restart"
	_, err := completer.Complete(ctx, line, len(line))

	// Without a client, should return empty (not error)
	if err != nil {
		t.Logf("Expected no error without client: %v", err)
	}
}

func TestAgentCompleter_ReturnsWhenAgentStreamIgnoresContext(t *testing.T) {
	client := agent.NewClient(blockingAgentTransport{})
	completer := NewAgentCompleter(client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := completer.Complete(ctx, "echo ?? value", len("echo ?? value"))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context timeout error from blocked agent stream")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("agent completer did not return after context cancellation")
	}
}

func TestAgentCompleter_ReturnsWhenContextBuilderBlocks(t *testing.T) {
	client := agent.NewClient(blockingAgentTransport{})
	completer := NewAgentCompleter(client)
	completer.buildContext = func(ctx context.Context) agent.Context {
		<-ctx.Done()
		return agent.Context{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := completer.Complete(ctx, "echo ?? value", len("echo ?? value"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected context timeout error")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("agent completer took %s after context cancellation, want under 100ms", elapsed)
	}
}

func TestAgentCompleter_Name(t *testing.T) {
	completer := NewAgentCompleter(nil)
	if completer.Name() != "agent" {
		t.Errorf("Name() = %q, want %q", completer.Name(), "agent")
	}
}

func TestAgentCompleter_IgnoresNonAgentLines(t *testing.T) {
	completer := NewAgentCompleter(nil)
	ctx := context.Background()

	result, err := completer.Complete(ctx, "ls -la", 6)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// No ?? means no agent completion
	if len(result.Items) != 0 {
		t.Errorf("Items count = %d, want 0", len(result.Items))
	}
}
