package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHTTPTransport_SendSuccess tests successful request/response cycle.
func TestHTTPTransport_SendSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}

		// Parse request body
		var req ollamaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}

		if req.Model != "codellama" {
			t.Errorf("expected model codellama, got %s", req.Model)
		}

		// Return success response
		resp := ollamaResponse{
			Response: "find . -name '*.go' -type f",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPConfig{
		URL:     server.URL,
		Model:   "codellama",
		Timeout: 5 * time.Second,
	})

	ctx := context.Background()
	textCh, errCh := transport.SendStreaming(ctx, Request{
		Prompt: "find all go files",
		Context: Context{
			Cwd: "/home/user/project",
		},
	})

	var text string
	for chunk := range textCh {
		text += chunk
	}
	for err := range errCh {
		if err != nil {
			t.Fatalf("SendStreaming() error = %v", err)
		}
	}

	// Use StreamCollector to parse the response
	collector := NewStreamCollector()
	collector.Append(text)
	resp := collector.Response()

	if resp.Type != ResponseTypeCommand {
		t.Errorf("expected command type, got %v", resp.Type)
	}
	if resp.Command != "find . -name '*.go' -type f" {
		t.Errorf("unexpected command: %q", resp.Command)
	}
}

// TestHTTPTransport_SendExplanation tests response that's an explanation.
func TestHTTPTransport_SendExplanation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaResponse{
			Response: "The find command searches for files in a directory hierarchy. You can use -name to match filenames and -type f to find only files.",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPConfig{
		URL:   server.URL,
		Model: "codellama",
	})

	ctx := context.Background()
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "explain find command"})

	var text string
	for chunk := range textCh {
		text += chunk
	}
	for err := range errCh {
		if err != nil {
			t.Fatalf("SendStreaming() error = %v", err)
		}
	}

	collector := NewStreamCollector()
	collector.Append(text)
	resp := collector.Response()

	if resp.Type != ResponseTypeExplanation {
		t.Errorf("expected explanation type, got %v", resp.Type)
	}
}

// TestHTTPTransport_SendAPIError tests handling of API errors.
func TestHTTPTransport_SendAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaResponse{
			Error: "model not found: codellama",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPConfig{
		URL:   server.URL,
		Model: "codellama",
	})

	ctx := context.Background()
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "test"})

	for range textCh {
	}

	var gotErr error
	for err := range errCh {
		if err != nil {
			gotErr = err
		}
	}

	if gotErr == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(gotErr.Error(), "model not found") {
		t.Errorf("expected model not found error, got %q", gotErr.Error())
	}
}

// TestHTTPTransport_SendHTTPError tests handling of HTTP errors.
func TestHTTPTransport_SendHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPConfig{
		URL:   server.URL,
		Model: "codellama",
	})

	ctx := context.Background()
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "test"})

	for range textCh {
	}

	var gotErr error
	for err := range errCh {
		if err != nil {
			gotErr = err
		}
	}

	if gotErr == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(gotErr.Error(), "500") {
		t.Errorf("expected HTTP 500 error, got %q", gotErr.Error())
	}
}

// TestHTTPTransport_SendInvalidJSON tests handling of invalid JSON response.
func TestHTTPTransport_SendInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json at all"))
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPConfig{
		URL:   server.URL,
		Model: "codellama",
	})

	ctx := context.Background()
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "test"})

	for range textCh {
	}

	var gotErr error
	for err := range errCh {
		if err != nil {
			gotErr = err
		}
	}

	if gotErr == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(gotErr.Error(), "parse response") {
		t.Errorf("expected parse error, got %q", gotErr.Error())
	}
}

// TestHTTPTransport_SendContextCanceled tests context cancellation.
func TestHTTPTransport_SendContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response - should be canceled
		time.Sleep(5 * time.Second)
		resp := ollamaResponse{Response: "too late", Done: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPConfig{
		URL:     server.URL,
		Model:   "codellama",
		Timeout: 10 * time.Second, // Long timeout
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "test"})

	for range textCh {
	}

	var gotErr error
	for err := range errCh {
		if err != nil {
			gotErr = err
		}
	}

	if gotErr == nil {
		t.Error("expected error for canceled context")
	}
}

// TestHTTPTransport_SendConnectionRefused tests connection refused error.
func TestHTTPTransport_SendConnectionRefused(t *testing.T) {
	transport := NewHTTPTransport(HTTPConfig{
		URL:     "http://localhost:59999", // Unlikely to be listening
		Model:   "codellama",
		Timeout: 1 * time.Second,
	})

	ctx := context.Background()
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "test"})

	for range textCh {
	}

	var gotErr error
	for err := range errCh {
		if err != nil {
			gotErr = err
		}
	}

	if gotErr == nil {
		t.Error("expected error for connection refused")
	}
}

// TestHTTPTransport_SendWithCustomHeaders tests custom headers are sent.
func TestHTTPTransport_SendWithCustomHeaders(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		resp := ollamaResponse{Response: "ok", Done: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPConfig{
		URL:   server.URL,
		Model: "codellama",
		Headers: map[string]string{
			"X-Custom-Header": "custom-value",
			"Authorization":   "Bearer token123",
		},
	})

	ctx := context.Background()
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "test"})
	for range textCh {
	}
	for range errCh {
	}

	if receivedHeaders.Get("X-Custom-Header") != "custom-value" {
		t.Errorf("custom header not received: %v", receivedHeaders)
	}
	if receivedHeaders.Get("Authorization") != "Bearer token123" {
		t.Errorf("auth header not received: %v", receivedHeaders)
	}
}

// TestHTTPTransport_BuildPromptAllFields tests prompt building with all context fields.
func TestHTTPTransport_BuildPromptAllFields(t *testing.T) {
	var receivedPrompt string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaRequest
		json.NewDecoder(r.Body).Decode(&req)
		receivedPrompt = req.Prompt
		resp := ollamaResponse{Response: "ok", Done: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPConfig{
		URL:   server.URL,
		Model: "codellama",
	})

	ctx := context.Background()
	textCh, errCh := transport.SendStreaming(ctx, Request{
		Prompt:      "find large files",
		CommandLine: "find . -size",
		Context: Context{
			Cwd:       "/home/user",
			GitBranch: "main",
			LastError: "permission denied",
		},
	})
	for range textCh {
	}
	for range errCh {
	}

	// Verify prompt contains context
	if !strings.Contains(receivedPrompt, "/home/user") {
		t.Error("prompt should contain cwd")
	}
	if !strings.Contains(receivedPrompt, "main") {
		t.Error("prompt should contain git branch")
	}
	if !strings.Contains(receivedPrompt, "permission denied") {
		t.Error("prompt should contain last error")
	}
	if !strings.Contains(receivedPrompt, "find . -size") {
		t.Error("prompt should contain partial command")
	}
	if !strings.Contains(receivedPrompt, "find large files") {
		t.Error("prompt should contain user request")
	}
}

// TestHTTPTransport_EmptyResponse tests handling of empty response.
func TestHTTPTransport_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaResponse{
			Response: "",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	transport := NewHTTPTransport(HTTPConfig{
		URL:   server.URL,
		Model: "codellama",
	})

	ctx := context.Background()
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "test"})

	var text string
	for chunk := range textCh {
		text += chunk
	}
	for range errCh {
	}

	// Empty response produces no text chunks
	if text != "" {
		t.Logf("Empty response produced text: %q", text)
	}
}
