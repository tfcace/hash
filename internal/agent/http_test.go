package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPTransport_New(t *testing.T) {
	cfg := HTTPConfig{
		URL:   "http://localhost:11434/api/generate",
		Model: "codellama",
	}

	transport := NewHTTPTransport(cfg)
	if transport == nil {
		t.Fatal("NewHTTPTransport() returned nil")
	}
	if transport.Name() != "http" {
		t.Errorf("Name() = %q, want %q", transport.Name(), "http")
	}
}

func TestHTTPTransport_Connect(t *testing.T) {
	cfg := HTTPConfig{
		URL:   "http://localhost:11434/api/generate",
		Model: "codellama",
	}

	transport := NewHTTPTransport(cfg)
	ctx := context.Background()

	// Connect should succeed (it's just initialization)
	err := transport.Connect(ctx)
	if err != nil {
		t.Errorf("Connect() error = %v", err)
	}
}

func TestHTTPTransport_SendToMockServer(t *testing.T) {
	// Create a mock server that returns an Ollama-style response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("Method = %q, want POST", r.Method)
		}

		// Return Ollama-style response
		resp := map[string]interface{}{
			"response": "find . -size +100M",
			"done":     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := HTTPConfig{
		URL:     server.URL,
		Model:   "codellama",
		Timeout: 5 * time.Second,
	}

	transport := NewHTTPTransport(cfg)
	ctx := context.Background()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	req := Request{
		Prompt: "find large files",
	}

	respCh, err := transport.Send(ctx, req)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Get response
	resp := <-respCh
	if resp.Type == ResponseTypeError {
		t.Errorf("Response is error: %s", resp.Error)
	}
	if resp.Command == "" && resp.Explanation == "" {
		t.Error("Response should have command or explanation")
	}
}

func TestHTTPTransport_Close(t *testing.T) {
	cfg := HTTPConfig{
		URL:   "http://localhost:11434/api/generate",
		Model: "codellama",
	}

	transport := NewHTTPTransport(cfg)

	// Close should not error
	err := transport.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestHTTPConfig_Defaults(t *testing.T) {
	cfg := HTTPConfig{
		URL: "http://localhost:11434/api/generate",
	}

	transport := NewHTTPTransport(cfg)

	// Should have default timeout
	if transport.config.Timeout == 0 {
		t.Error("Should have default timeout")
	}
}
