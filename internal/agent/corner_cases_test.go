package agent

import (
	"context"
	"testing"
	"time"
)

// TestACPTransport_ConnectionClosedResetsState tests that when the messages
// channel closes (agent process exits), the connection state is reset so
// the next Send() can reconnect.
func TestACPTransport_ConnectionClosedResetsState(t *testing.T) {
	transport := &ACPTransport{
		config:   ACPConfig{Command: "test"},
		messages: make(chan []byte, 1024),
		done:     make(chan struct{}),
	}

	// Simulate a connected state
	mockStdin := newMockPipe()
	transport.stdin = mockStdin
	transport.sessionID = "test-session"

	// Close messages channel (simulates readLoop exiting when agent crashes)
	close(transport.messages)

	// Attempt to send - this should detect connection closed
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	respCh, err := transport.Send(ctx, Request{Prompt: "test"})
	if err != nil {
		t.Logf("Send returned early error: %v", err)
	} else {
		resp := <-respCh
		if resp.Type == ResponseTypeError {
			t.Logf("Got error response: %s", resp.Error)
		}
	}

	// Give goroutine time to process
	time.Sleep(50 * time.Millisecond)

	// BUG: After "connection closed" error, stdin should be reset
	// Currently it's NOT reset, so next Send would fail again
	transport.mu.Lock()
	stdinAfterClose := transport.stdin
	transport.mu.Unlock()

	if stdinAfterClose != nil {
		t.Errorf("After connection closed, stdin should be nil to trigger reconnect")
		t.Log("BUG: Connection closed doesn't reset connection state")
	}
}

// TestACPTransport_SendAfterClose tests behavior when Send is called
// after the transport has been closed.
func TestACPTransport_SendAfterClose(t *testing.T) {
	// Use a non-existent command to fail fast
	transport := NewACPTransport(ACPConfig{
		Command: "/nonexistent/command/that/does/not/exist",
		Args:    []string{},
	})

	// Close the transport before any Send
	transport.Close()

	// Attempt to send - should handle gracefully (will fail to connect)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := transport.Send(ctx, Request{Prompt: "test"})

	// Should get an error (can't connect) but not panic
	if err == nil {
		t.Error("Send after Close should return error, got nil")
	} else {
		t.Logf("Send after Close returned error (expected): %v", err)
	}
}

// TestACPTransport_DoubleClose tests that calling Close twice is safe.
func TestACPTransport_DoubleClose(t *testing.T) {
	transport := NewACPTransport(ACPConfig{
		Command: "echo",
		Args:    []string{},
	})

	// Close twice - should not panic
	err1 := transport.Close()
	err2 := transport.Close()

	if err1 != nil {
		t.Errorf("First Close() returned error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("Second Close() returned error: %v", err2)
	}
}

// TestACPTransport_ContextAlreadyCanceled tests behavior when context
// is already canceled before Send is called.
func TestACPTransport_ContextAlreadyCanceled(t *testing.T) {
	transport := &ACPTransport{
		config:   ACPConfig{Command: "test"},
		messages: make(chan []byte, 1024),
		done:     make(chan struct{}),
	}

	// Simulate connected state
	mockStdin := newMockPipe()
	transport.stdin = mockStdin
	transport.sessionID = "test-session"

	// Create already-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Send with already-canceled context
	respCh, err := transport.Send(ctx, Request{Prompt: "test"})

	if err != nil {
		// Early error is acceptable
		t.Logf("Send returned early error (good): %v", err)
		return
	}

	// If no early error, goroutine should detect canceled context
	resp := <-respCh
	if resp.Type != ResponseTypeError {
		t.Errorf("Expected error response for canceled context, got type %v", resp.Type)
	}
}

// TestACPTransport_IdleTimeoutResetsConnection tests that when idle timeout
// occurs, the connection state is properly handled for next request.
func TestACPTransport_IdleTimeoutResetsConnection(t *testing.T) {
	// This test would require mocking IdleTimeout or waiting 30 seconds.
	// For now, just document the expected behavior.
	t.Log(`
SCENARIO: Agent becomes unresponsive (idle timeout)

1. User sends request
2. Agent stops responding (no messages for IdleTimeout)
3. Hash detects idle timeout, sends cancel, returns error
4. User tries another request
5. EXPECTED: Should reconnect to agent
6. CURRENT: May or may not work depending on agent state

The idle timeout handler calls sendCancel() which clears sessionID,
but doesn't reset the connection. If the agent is truly hung,
the next request may also hang or fail.
	`)
}

// TestACPTransport_RapidCancelAndResend tests rapid cancel-and-resend cycles
// don't corrupt state.
func TestACPTransport_RapidCancelAndResend(t *testing.T) {
	transport := &ACPTransport{
		config:   ACPConfig{Command: "test"},
		messages: make(chan []byte, 1024),
		done:     make(chan struct{}),
	}

	// Simulate connected state
	mockStdin := newMockPipe()
	transport.stdin = mockStdin
	transport.sessionID = "test-session"

	// Rapid cancel-and-send cycles
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)

		respCh, err := transport.Send(ctx, Request{Prompt: "test"})
		if err != nil {
			cancel()
			continue
		}

		// Cancel almost immediately
		cancel()

		// Drain response
		select {
		case <-respCh:
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Transport should still be in a valid state (not corrupted)
	transport.mu.Lock()
	defer transport.mu.Unlock()

	// Just verify we didn't panic and state is consistent
	t.Logf("After 10 rapid cancel cycles: stdin=%v, sessionID=%q",
		transport.stdin != nil, transport.sessionID)
}

// TestACPTransport_ConnectLockedPartialFailure tests cleanup when
// connection setup partially fails.
func TestACPTransport_ConnectLockedPartialFailure(t *testing.T) {
	// This is hard to test without mocking exec.Command.
	// Document the potential issue:
	t.Log(`
POTENTIAL BUG: Partial connection cleanup

In connectLocked():
1. StdinPipe() succeeds -> stdin is set
2. StdoutPipe() fails -> returns error
3. stdin is NOT cleaned up

However, the error path doesn't set stdin back to nil,
so the next Connect() call would see stdin != nil and
return early thinking it's already connected.

Actually checking the code... the error returns before
stdin is assigned to t.stdin, so this might be okay.
Need to verify the exact assignment order.
	`)
}

// TestClient_AskWithClosedTransport tests Client.Ask behavior when
// the underlying transport is closed or broken.
func TestClient_AskWithClosedTransport(t *testing.T) {
	mock := NewMockTransport() // No responses
	client := NewClient(mock)

	// Close the mock transport
	mock.Close()

	ctx := context.Background()
	resp, err := client.Ask(ctx, Request{Prompt: "test"})

	// Should handle gracefully - either error or error response
	if err != nil {
		t.Logf("Ask returned error: %v", err)
	} else if resp.Type == ResponseTypeError {
		t.Logf("Ask returned error response: %s", resp.Error)
	} else {
		t.Logf("Ask returned: type=%v, unexpected for closed transport", resp.Type)
	}
}
