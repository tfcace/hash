package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type discardWriteCloser struct {
	io.Writer
}

func (d discardWriteCloser) Close() error {
	return nil
}

func TestACPTransport_HandleRequestPermission_Allow(t *testing.T) {
	// Create pipes for mock communication
	clientRead, agentWrite := io.Pipe()
	agentRead, clientWrite := io.Pipe()

	transport := &ACPTransport{
		stdin:    clientWrite,
		stdout:   clientRead,
		reader:   bufio.NewReader(clientRead),
		messages: make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	// Set up permission handler that allows
	var handlerCalled bool
	var handlerCommand string
	var mu sync.Mutex
	transport.SetPermissionHandler(func(_ context.Context, req ToolPermissionRequest) (bool, bool) {
		mu.Lock()
		handlerCalled = true
		handlerCommand = req.Command
		mu.Unlock()
		return true, false // allow once
	})

	// Simulate calling handleRequestPermission directly
	params := `{"sessionId":"test","toolCall":{"toolCallId":"123","title":"kubectl get pods","rawInput":{}}}`

	// Start goroutine to read response
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 1024)
		n, err := agentRead.Read(buf)
		if err == nil {
			done <- string(buf[:n])
		} else {
			done <- ""
		}
	}()

	// Call the handler
	transport.handleRequestPermission(context.Background(), 1, json.RawMessage(params))

	// Wait for response with timeout
	select {
	case response := <-done:
		if !strings.Contains(response, `"outcome":"selected"`) {
			t.Errorf("Expected selected outcome, got: %s", response)
		}
		if !strings.Contains(response, `"optionId":"allow"`) {
			t.Errorf("Expected allow optionId, got: %s", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}

	mu.Lock()
	called := handlerCalled
	cmd := handlerCommand
	mu.Unlock()

	if !called {
		t.Error("Permission handler was not called")
	}
	if cmd != "kubectl get pods" {
		t.Errorf("Handler received wrong command: %s", cmd)
	}

	// Cleanup
	agentWrite.Close()
	clientWrite.Close()
	_ = agentRead
	_ = clientRead
}

func TestACPTransport_HandleRequestPermission_CancelUnblocksHandler(t *testing.T) {
	_, agentWrite := io.Pipe()
	agentRead, clientWrite := io.Pipe()
	defer agentWrite.Close()  //nolint:errcheck
	defer clientWrite.Close() //nolint:errcheck

	transport := &ACPTransport{
		stdin:    clientWrite,
		messages: make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The handler blocks like a real prompt waiting for a keypress.
	// Canceling the turn must propagate through the handler's context so
	// it can release the terminal instead of blocking forever.
	handlerUnblocked := make(chan struct{})
	transport.SetPermissionHandler(func(hctx context.Context, req ToolPermissionRequest) (bool, bool) {
		select {
		case <-hctx.Done():
			close(handlerUnblocked)
		case <-time.After(5 * time.Second):
		}
		return false, false
	})

	responseCh := make(chan string, 1)
	go func() {
		buf := make([]byte, 1024)
		n, err := agentRead.Read(buf)
		if err != nil {
			responseCh <- ""
			return
		}
		responseCh <- string(buf[:n])
	}()

	params := `{"sessionId":"test","toolCall":{"toolCallId":"123","title":"rm -rf /tmp/x","rawInput":{}}}`
	go transport.handleRequestPermission(ctx, 1, json.RawMessage(params))
	cancel()

	select {
	case <-handlerUnblocked:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not observe turn cancellation")
	}

	select {
	case response := <-responseCh:
		if !strings.Contains(response, `"optionId":"reject`) {
			t.Errorf("expected reject response after cancellation, got: %s", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for deny response")
	}
}

func TestACPTransport_HandleRequestPermission_AlwaysAllow(t *testing.T) {
	// Create pipes for mock communication
	_, agentWrite := io.Pipe()
	agentRead, clientWrite := io.Pipe()

	transport := &ACPTransport{
		stdin:    clientWrite,
		messages: make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	// Set up permission handler that returns "always allow"
	transport.SetPermissionHandler(func(_ context.Context, req ToolPermissionRequest) (bool, bool) {
		return true, true // allow always
	})

	params := `{"sessionId":"test","toolCall":{"toolCallId":"123","title":"git status","rawInput":{}}}`

	// Start goroutine to read response
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 1024)
		n, err := agentRead.Read(buf)
		if err == nil {
			done <- string(buf[:n])
		} else {
			done <- ""
		}
	}()

	// Call the handler
	transport.handleRequestPermission(context.Background(), 1, json.RawMessage(params))

	// Wait for response with timeout
	select {
	case response := <-done:
		if !strings.Contains(response, `"optionId":"allow_always"`) {
			t.Errorf("Expected allow_always optionId, got: %s", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}

	// Cleanup
	agentWrite.Close()
	clientWrite.Close()
}

func TestACPTransport_HandleRequestPermission_Deny(t *testing.T) {
	// Create pipes for mock communication
	_, agentWrite := io.Pipe()
	agentRead, clientWrite := io.Pipe()

	transport := &ACPTransport{
		stdin:    clientWrite,
		messages: make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	// Set up permission handler that denies
	transport.SetPermissionHandler(func(_ context.Context, req ToolPermissionRequest) (bool, bool) {
		return false, false // deny
	})

	params := `{"sessionId":"test","toolCall":{"toolCallId":"123","title":"rm -rf /","rawInput":{}}}`

	// Start goroutine to read response
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 1024)
		n, err := agentRead.Read(buf)
		if err == nil {
			done <- string(buf[:n])
		} else {
			done <- ""
		}
	}()

	// Call the handler
	transport.handleRequestPermission(context.Background(), 1, json.RawMessage(params))

	// Wait for response with timeout
	select {
	case response := <-done:
		if !strings.Contains(response, `"optionId":"reject"`) {
			t.Errorf("Expected reject optionId, got: %s", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}

	// Cleanup
	agentWrite.Close()
	clientWrite.Close()
}

func TestACPTransport_HandleRequestPermission_NoHandler(t *testing.T) {
	// Create pipes for mock communication
	_, agentWrite := io.Pipe()
	agentRead, clientWrite := io.Pipe()

	transport := &ACPTransport{
		stdin:    clientWrite,
		messages: make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	// No permission handler set - should default to deny

	params := `{"sessionId":"test","toolCall":{"toolCallId":"123","title":"echo hello","rawInput":{}}}`

	// Start goroutine to read response
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 1024)
		n, err := agentRead.Read(buf)
		if err == nil {
			done <- string(buf[:n])
		} else {
			done <- ""
		}
	}()

	// Call the handler
	transport.handleRequestPermission(context.Background(), 1, json.RawMessage(params))

	// Wait for response with timeout
	select {
	case response := <-done:
		// With no handler, should default to reject
		if !strings.Contains(response, `"optionId":"reject"`) {
			t.Errorf("Expected reject optionId when no handler, got: %s", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}

	// Cleanup
	agentWrite.Close()
	clientWrite.Close()
}

func TestACPTransport_HandleRequestPermission_ExtractFromRawInput(t *testing.T) {
	// Test extraction of command from rawInput when title is empty
	_, agentWrite := io.Pipe()
	agentRead, clientWrite := io.Pipe()

	transport := &ACPTransport{
		stdin:    clientWrite,
		messages: make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	var receivedCommand string
	transport.SetPermissionHandler(func(_ context.Context, req ToolPermissionRequest) (bool, bool) {
		receivedCommand = req.Command
		return true, false
	})

	// Params with empty title but command in rawInput
	params := `{"sessionId":"test","toolCall":{"toolCallId":"123","title":"","rawInput":{"command":"ls -la"}}}`

	// Start goroutine to read response
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		agentRead.Read(buf)
		close(done)
	}()

	// Call the handler
	transport.handleRequestPermission(context.Background(), 1, json.RawMessage(params))

	// Wait for response
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}

	if receivedCommand != "ls -la" {
		t.Errorf("Expected command from rawInput, got: %s", receivedCommand)
	}

	// Cleanup
	agentWrite.Close()
	clientWrite.Close()
}

func TestACPTransport_HandleRequestPermission_UsesAgentOptionIDs(t *testing.T) {
	// When the agent provides custom option IDs, the response should use them
	// instead of the hardcoded defaults.
	_, agentWrite := io.Pipe()
	agentRead, clientWrite := io.Pipe()

	transport := &ACPTransport{
		stdin:    clientWrite,
		messages: make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	transport.SetPermissionHandler(func(_ context.Context, req ToolPermissionRequest) (bool, bool) {
		return true, false // allow once
	})

	// Agent provides custom option IDs
	params := `{"sessionId":"test","toolCall":{"toolCallId":"456","title":"npm install","rawInput":{}},"options":[{"kind":"allow_once","name":"Allow","optionId":"custom_allow_id"},{"kind":"reject_once","name":"Deny","optionId":"custom_reject_id"}]}`

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 1024)
		n, err := agentRead.Read(buf)
		if err == nil {
			done <- string(buf[:n])
		} else {
			done <- ""
		}
	}()

	transport.handleRequestPermission(context.Background(), 1, json.RawMessage(params))

	select {
	case response := <-done:
		// Should use the agent's custom option ID, not the default "allow"
		if !strings.Contains(response, `"optionId":"custom_allow_id"`) {
			t.Errorf("Expected agent-provided optionId 'custom_allow_id', got: %s", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}

	agentWrite.Close()
	clientWrite.Close()
}

func TestACPTransport_HandleRequestPermission_EmptyCommandAutoRejects(t *testing.T) {
	// When both title and rawInput.command are empty, should auto-reject
	// without calling the permission handler.
	_, agentWrite := io.Pipe()
	agentRead, clientWrite := io.Pipe()

	transport := &ACPTransport{
		stdin:    clientWrite,
		messages: make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	handlerCalled := false
	transport.SetPermissionHandler(func(_ context.Context, req ToolPermissionRequest) (bool, bool) {
		handlerCalled = true
		return true, false
	})

	// Both title and rawInput are empty
	params := `{"sessionId":"test","toolCall":{"toolCallId":"789","title":"","rawInput":{}}}`

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 1024)
		n, err := agentRead.Read(buf)
		if err == nil {
			done <- string(buf[:n])
		} else {
			done <- ""
		}
	}()

	transport.handleRequestPermission(context.Background(), 1, json.RawMessage(params))

	select {
	case response := <-done:
		if !strings.Contains(response, `"optionId":"reject"`) {
			t.Errorf("Expected reject for empty command, got: %s", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}

	if handlerCalled {
		t.Error("Permission handler should not be called for empty commands")
	}

	agentWrite.Close()
	clientWrite.Close()
}

func TestACPTransport_HandleRequestPermission_ExtractsToolName(t *testing.T) {
	// Tool name should be extracted from rawInput and passed to the handler.
	_, agentWrite := io.Pipe()
	agentRead, clientWrite := io.Pipe()

	transport := &ACPTransport{
		stdin:    clientWrite,
		messages: make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	var receivedReq ToolPermissionRequest
	transport.SetPermissionHandler(func(_ context.Context, req ToolPermissionRequest) (bool, bool) {
		receivedReq = req
		return true, false
	})

	// rawInput includes a "tool" field
	params := `{"sessionId":"test","toolCall":{"toolCallId":"abc","title":"cat /etc/passwd","rawInput":{"tool":"Read","command":"cat /etc/passwd"}}}`

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		agentRead.Read(buf)
		close(done)
	}()

	transport.handleRequestPermission(context.Background(), 1, json.RawMessage(params))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}

	if receivedReq.ToolName != "Read" {
		t.Errorf("Expected tool name 'Read', got: %q", receivedReq.ToolName)
	}
	if receivedReq.Command != "cat /etc/passwd" {
		t.Errorf("Expected command 'cat /etc/passwd', got: %q", receivedReq.Command)
	}
	if receivedReq.ToolCallID != "abc" {
		t.Errorf("Expected toolCallId 'abc', got: %q", receivedReq.ToolCallID)
	}

	agentWrite.Close()
	clientWrite.Close()
}

func TestACPTransport_HandleRequestPermission_SerializesPrompts(t *testing.T) {
	transport := &ACPTransport{
		stdin: discardWriteCloser{Writer: io.Discard},
	}

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32

	transport.SetPermissionHandler(func(_ context.Context, req ToolPermissionRequest) (bool, bool) {
		current := inFlight.Add(1)
		for {
			observed := maxInFlight.Load()
			if current <= observed || maxInFlight.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		inFlight.Add(-1)
		return true, false
	})

	params1 := json.RawMessage(`{"sessionId":"test-session","toolCall":{"toolCallId":"1","title":"git status","rawInput":{}}}`)
	params2 := json.RawMessage(`{"sessionId":"test-session","toolCall":{"toolCallId":"2","title":"npm test","rawInput":{}}}`)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		transport.handleRequestPermission(context.Background(), 1, params1)
	}()

	go func() {
		defer wg.Done()
		<-start
		transport.handleRequestPermission(context.Background(), 2, params2)
	}()

	close(start)
	wg.Wait()

	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("permission handler ran concurrently: max in-flight = %d, want 1", got)
	}
}

func TestExtractToolName(t *testing.T) {
	tests := []struct {
		name     string
		rawInput string
		want     string
	}{
		{"empty input", `{}`, ""},
		{"tool field", `{"tool":"Bash"}`, "Bash"},
		{"name field", `{"name":"Write"}`, "Write"},
		{"tool takes priority over name", `{"tool":"Read","name":"Other"}`, "Read"},
		{"null input", ``, ""},
		{"invalid json", `not json`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractToolName(json.RawMessage(tt.rawInput))
			if got != tt.want {
				t.Errorf("extractToolName(%q) = %q, want %q", tt.rawInput, got, tt.want)
			}
		})
	}
}

func TestResolveOptionID(t *testing.T) {
	tests := []struct {
		name     string
		options  []permissionOption
		kind     string
		fallback string
		want     string
	}{
		{
			name:     "no options uses fallback",
			options:  nil,
			kind:     "allow_once",
			fallback: "allow",
			want:     "allow",
		},
		{
			name: "matches kind to optionId",
			options: []permissionOption{
				{Kind: "allow_once", OptionID: "yes_please"},
				{Kind: "reject_once", OptionID: "no_thanks"},
			},
			kind:     "allow_once",
			fallback: "allow",
			want:     "yes_please",
		},
		{
			name: "no matching kind uses fallback",
			options: []permissionOption{
				{Kind: "allow_once", OptionID: "yes"},
			},
			kind:     "allow_always",
			fallback: "allow_always",
			want:     "allow_always",
		},
		{
			name:     "empty options uses fallback",
			options:  []permissionOption{},
			kind:     "reject_once",
			fallback: "reject",
			want:     "reject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOptionID(tt.options, tt.kind, tt.fallback)
			if got != tt.want {
				t.Errorf("resolveOptionID() = %q, want %q", got, tt.want)
			}
		})
	}
}
