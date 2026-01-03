package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPConfig configures the HTTP transport.
type HTTPConfig struct {
	URL     string        // API endpoint URL
	Model   string        // Model name (e.g., "codellama")
	Timeout time.Duration // Request timeout
	Headers map[string]string // Optional custom headers
}

// HTTPTransport communicates with an agent via HTTP REST API.
// Supports Ollama, vLLM, and similar APIs.
type HTTPTransport struct {
	config HTTPConfig
	client *http.Client
}

// NewHTTPTransport creates a new HTTP transport.
func NewHTTPTransport(cfg HTTPConfig) *HTTPTransport {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	return &HTTPTransport{
		config: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// Name returns the transport name.
func (t *HTTPTransport) Name() string {
	return "http"
}

// Connect initializes the HTTP client (no-op for HTTP).
func (t *HTTPTransport) Connect(ctx context.Context) error {
	// HTTP is stateless, nothing to connect
	return nil
}

// ollamaRequest is the request format for Ollama API.
type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// ollamaResponse is the response format for Ollama API.
type ollamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
}

// Send sends a request to the HTTP API.
func (t *HTTPTransport) Send(ctx context.Context, req Request) (<-chan Response, error) {
	respCh := make(chan Response, 1)

	go func() {
		defer close(respCh)

		// Build prompt with context
		prompt := t.buildPrompt(req)

		// Create Ollama-style request
		ollamaReq := ollamaRequest{
			Model:  t.config.Model,
			Prompt: prompt,
			Stream: false,
		}

		body, err := json.Marshal(ollamaReq)
		if err != nil {
			respCh <- Response{
				Type:  ResponseTypeError,
				Error: fmt.Sprintf("marshal request: %v", err),
			}
			return
		}

		// Create HTTP request
		httpReq, err := http.NewRequestWithContext(ctx, "POST", t.config.URL, bytes.NewReader(body))
		if err != nil {
			respCh <- Response{
				Type:  ResponseTypeError,
				Error: fmt.Sprintf("create request: %v", err),
			}
			return
		}

		httpReq.Header.Set("Content-Type", "application/json")
		for k, v := range t.config.Headers {
			httpReq.Header.Set(k, v)
		}

		// Send request
		httpResp, err := t.client.Do(httpReq)
		if err != nil {
			respCh <- Response{
				Type:  ResponseTypeError,
				Error: fmt.Sprintf("http request: %v", err),
			}
			return
		}
		defer httpResp.Body.Close()

		// Read response
		respBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			respCh <- Response{
				Type:  ResponseTypeError,
				Error: fmt.Sprintf("read response: %v", err),
			}
			return
		}

		if httpResp.StatusCode != http.StatusOK {
			respCh <- Response{
				Type:  ResponseTypeError,
				Error: fmt.Sprintf("http status %d: %s", httpResp.StatusCode, string(respBody)),
			}
			return
		}

		// Parse Ollama response
		var ollamaResp ollamaResponse
		if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
			respCh <- Response{
				Type:  ResponseTypeError,
				Error: fmt.Sprintf("parse response: %v", err),
			}
			return
		}

		if ollamaResp.Error != "" {
			respCh <- Response{
				Type:  ResponseTypeError,
				Error: ollamaResp.Error,
			}
			return
		}

		// Parse the response text to determine type
		respCh <- t.parseResponse(ollamaResp.Response)
	}()

	return respCh, nil
}

// buildPrompt constructs the prompt with context.
func (t *HTTPTransport) buildPrompt(req Request) string {
	var b strings.Builder

	b.WriteString("You are a shell command assistant. ")
	b.WriteString("When asked for a command, respond with ONLY the command, no explanation. ")
	b.WriteString("When asked for an explanation, provide a clear explanation.\n\n")

	// Add context
	if req.Context.Cwd != "" {
		b.WriteString(fmt.Sprintf("Current directory: %s\n", req.Context.Cwd))
	}
	if req.Context.GitBranch != "" {
		b.WriteString(fmt.Sprintf("Git branch: %s\n", req.Context.GitBranch))
	}
	if req.Context.LastError != "" {
		b.WriteString(fmt.Sprintf("Last error:\n%s\n", req.Context.LastError))
	}
	if req.CommandLine != "" {
		b.WriteString(fmt.Sprintf("Partial command: %s\n", req.CommandLine))
	}

	b.WriteString(fmt.Sprintf("\nUser request: %s\n", req.Prompt))

	return b.String()
}

// parseResponse determines the response type from the text.
func (t *HTTPTransport) parseResponse(text string) Response {
	text = strings.TrimSpace(text)

	// If it looks like a command (starts with common command prefixes or is short)
	if looksLikeCommand(text) {
		return Response{
			Type:    ResponseTypeCommand,
			Command: text,
		}
	}

	return Response{
		Type:        ResponseTypeExplanation,
		Explanation: text,
	}
}

// looksLikeCommand checks if the text appears to be a shell command.
func looksLikeCommand(text string) bool {
	// Single line, no markdown, looks like a command
	if strings.Contains(text, "\n") && len(text) > 100 {
		return false
	}

	// Common command prefixes
	prefixes := []string{
		"find ", "grep ", "ls ", "cat ", "echo ", "cd ", "mkdir ", "rm ",
		"cp ", "mv ", "chmod ", "chown ", "docker ", "kubectl ", "git ",
		"npm ", "yarn ", "go ", "python ", "pip ", "curl ", "wget ",
		"sed ", "awk ", "sort ", "uniq ", "head ", "tail ", "tar ",
		"sudo ", "./", "/",
	}

	lower := strings.ToLower(text)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	// Short responses without spaces are likely commands
	if len(text) < 80 && !strings.Contains(text, ". ") {
		return true
	}

	return false
}

// Close closes the HTTP client (no-op for HTTP).
func (t *HTTPTransport) Close() error {
	// HTTP is stateless, nothing to close
	return nil
}

// Compile-time check that HTTPTransport implements Transport
var _ Transport = (*HTTPTransport)(nil)
