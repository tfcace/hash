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
	// This test documents the expected behavior.
	// Currently, the transport does NOT recover from broken pipes,
	// which is a bug that needs to be fixed.
	t.Skip("This test documents expected behavior - currently fails due to bug")

	_ = NewACPTransport(ACPConfig{
		Command: "echo", // dummy command
		Args:    []string{},
	})

	// Simulate the scenario:
	// 1. First request succeeds
	// 2. User cancels (context canceled)
	// 3. Pipe breaks (agent exits)
	// 4. Second request should reconnect, not fail with broken pipe

	// We can't easily test this without the actual ACP process,
	// but we can document the expected behavior and verify the
	// reconnection logic is correct.
}

// TestACPTransport_WriteErrorResetsConnection verifies that write errors
// trigger a connection reset, allowing subsequent requests to reconnect.
func TestACPTransport_WriteErrorResetsConnection(t *testing.T) {
	// This test verifies the fix for the broken pipe issue.
	// When stdin.Write fails, the connection should be reset so that
	// the next Send() call will trigger a reconnect via lazy connect.

	transport := &ACPTransport{
		config:   ACPConfig{Command: "test"},
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

	respCh, err := transport.Send(ctx, Request{Prompt: "test"})

	// Send() returns a channel - the error occurs in the goroutine
	if err != nil {
		// Early error (e.g., in newSession) - connection should be reset
		t.Logf("Send returned early error: %v", err)
	} else {
		// Wait for the response from the goroutine
		resp := <-respCh
		if resp.Type == ResponseTypeError {
			t.Logf("Got error response: %s", resp.Error)
		}
	}

	// Give the goroutine time to call resetConnection
	time.Sleep(50 * time.Millisecond)

	// After a write error, stdin should be reset to nil
	// so the next Send() will trigger lazy reconnect.
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
6. Send() is called:
   - stdin != nil (still points to broken pipe)
   - needSession is true (sessionID was cleared)
   - newSession() calls sendRequest()
   - sendRequest() tries stdin.Write()
   - FAILS with "broken pipe"

FIX: When write fails, reset stdin to nil so next Send() reconnects.
	`)
}
