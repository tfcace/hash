package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	transport.SetPermissionHandler(func(command string) (bool, bool) {
		mu.Lock()
		handlerCalled = true
		handlerCommand = command
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
	transport.handleRequestPermission(1, json.RawMessage(params))

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
	transport.SetPermissionHandler(func(command string) (bool, bool) {
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
	transport.handleRequestPermission(1, json.RawMessage(params))

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
	transport.SetPermissionHandler(func(command string) (bool, bool) {
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
	transport.handleRequestPermission(1, json.RawMessage(params))

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
	transport.handleRequestPermission(1, json.RawMessage(params))

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
	transport.SetPermissionHandler(func(command string) (bool, bool) {
		receivedCommand = command
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
	transport.handleRequestPermission(1, json.RawMessage(params))

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
