package agent

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

// mockPipe simulates a pipe that can be broken
type mockPipe struct {
	mu       sync.Mutex
	broken   bool
	written  []byte
	closedCh chan struct{}
}

func newMockPipe() *mockPipe {
	return &mockPipe{
		closedCh: make(chan struct{}),
	}
}

func (p *mockPipe) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.broken {
		return 0, io.ErrClosedPipe
	}
	p.written = append(p.written, data...)
	return len(data), nil
}

func (p *mockPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.broken = true
	select {
	case <-p.closedCh:
	default:
		close(p.closedCh)
	}
	return nil
}

func (p *mockPipe) Break() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.broken = true
}

func (p *mockPipe) IsBroken() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.broken
}

// TestACPTransport_ReconnectAfterBrokenPipe tests that the transport
// recovers after the pipe breaks (e.g., after context cancellation).
//
// This is a critical stability test: when a user cancels a request (Ctrl+C),
// the agent process may exit, breaking the pipe. Subsequent requests must
// not fail with "broken pipe" - they should reconnect automatically.
func TestACPTransport_ReconnectAfterBrokenPipe(t *testing.T) {
	// We can't fully integration-test reconnect here without a real ACP process,
	// but we can assert the scenario no longer depends on caller retries.
	transport := &ACPTransport{
		config:   ACPConfig{Command: "/nonexistent/acp-agent"},
		messages: make(chan []byte, 1024),
		done:     make(chan struct{}),
	}

	// Simulate a connected state with a broken pipe and no session (typical
	// state after sendCancel during Ctrl+C).
	mockStdin := newMockPipe()
	mockStdin.Break()
	transport.stdin = mockStdin
	transport.sessionID = ""

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "test"})
	for range textCh {
	}
	for range errCh {
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.stdin != nil {
		t.Fatal("expected broken connection to be reset for reconnect")
	}
}

// TestACPTransport_WriteErrorResetsConnection verifies that write errors
// trigger a connection reset, allowing subsequent requests to reconnect.
func TestACPTransport_WriteErrorResetsConnection(t *testing.T) {
	// This test verifies the fix for the broken pipe issue.
	// When stdin.Write fails, the connection should be reset so that
	// the next SendStreaming() call will trigger a reconnect via lazy connect.

	transport := &ACPTransport{
		config:   ACPConfig{Command: "/nonexistent/acp-agent"},
		messages: make(chan []byte, 1024),
		done:     make(chan struct{}),
	}

	// Simulate a connected state with a broken pipe
	mockStdin := newMockPipe()
	transport.stdin = mockStdin
	transport.sessionID = "test-session"

	// Break the pipe (simulates agent process exiting after cancel)
	mockStdin.Break()

	// Attempt to send - this should detect the broken pipe
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "test"})

	// Drain channels
	for range textCh {
	}

	var gotErr error
	for err := range errCh {
		if err != nil {
			gotErr = err
		}
	}

	if gotErr != nil {
		t.Logf("Got error (expected): %v", gotErr)
	}

	// Give the goroutine time to call resetConnection
	time.Sleep(50 * time.Millisecond)

	// After a write error, stdin should be reset to nil
	// so the next SendStreaming() will trigger lazy reconnect.
	transport.mu.Lock()
	stdinAfterError := transport.stdin
	transport.mu.Unlock()

	if stdinAfterError != nil {
		t.Errorf("After write error, stdin should be nil to trigger reconnect, but it's not nil")
	}
}

// TestACPTransport_BrokenPipeScenario documents the exact user scenario
// that causes the "broken pipe" error.
func TestACPTransport_BrokenPipeScenario(t *testing.T) {
	t.Log(`
SCENARIO: User cancels request, next request fails with broken pipe

1. User runs: ?? move jj pointer to master
2. User presses Ctrl+C (context canceled)
3. sendCancel() is called:
   - sessionID is cleared
   - cancel notification sent to agent
4. Agent process may exit after receiving cancel
5. User runs: ?? jj move pointer to master bookmark
6. SendStreaming() is called:
   - stdin != nil (still points to broken pipe)
   - needSession is true (sessionID was cleared)
   - newSession() calls sendRequest()
   - sendRequest() tries stdin.Write()
   - FAILS with "broken pipe"

FIX: When write fails, reset stdin to nil so next SendStreaming() reconnects.
	`)
}
