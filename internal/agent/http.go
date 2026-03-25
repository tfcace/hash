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
	URL     string            // API endpoint URL
	Model   string            // Model name (e.g., "codellama")
	Timeout time.Duration     // Request timeout
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

// SendStreaming sends a request to the HTTP API and streams the response text.
// HTTP transport is inherently non-streaming (Stream: false), so the full
// response text is emitted as a single chunk on the text channel.
//
//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (t *HTTPTransport) SendStreaming(ctx context.Context, req Request) (<-chan string, <-chan error) {
	textCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(textCh)
		defer close(errCh)

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
			errCh <- fmt.Errorf("marshal request: %v", err)
			return
		}

		// Create HTTP request
		httpReq, err := http.NewRequestWithContext(ctx, "POST", t.config.URL, bytes.NewReader(body))
		if err != nil {
			errCh <- fmt.Errorf("create request: %v", err)
			return
		}

		httpReq.Header.Set("Content-Type", "application/json")
		for k, v := range t.config.Headers {
			httpReq.Header.Set(k, v)
		}

		// Send request
		httpResp, err := t.client.Do(httpReq)
		if err != nil {
			errCh <- fmt.Errorf("http request: %v", err)
			return
		}
		defer httpResp.Body.Close()

		// Read response
		respBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			errCh <- fmt.Errorf("read response: %v", err)
			return
		}

		if httpResp.StatusCode != http.StatusOK {
			errCh <- fmt.Errorf("http status %d: %s", httpResp.StatusCode, string(respBody))
			return
		}

		// Parse Ollama response
		var ollamaResp ollamaResponse
		if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
			errCh <- fmt.Errorf("parse response: %v", err)
			return
		}

		if ollamaResp.Error != "" {
			errCh <- fmt.Errorf("%s", ollamaResp.Error)
			return
		}

		// Emit the full response text as a single chunk
		if ollamaResp.Response != "" {
			textCh <- ollamaResp.Response
		}
	}()

	return textCh, errCh
}

// buildPrompt constructs the prompt with context.
func (t *HTTPTransport) buildPrompt(req Request) string {
	var b strings.Builder

	b.WriteString("You are a shell command assistant. ")
	b.WriteString("When asked for a command, respond with ONLY the command, no explanation. ")
	b.WriteString("When asked for an explanation, provide a clear explanation. ")
	b.WriteString("\n")

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

// looksLikeCommand checks if the text appears to be a shell command.
//
//nolint:gocyclo // heuristic pattern matching requires multiple checks
func looksLikeCommand(text string) bool {
	// Multi-line text is likely an explanation
	if strings.Contains(text, "\n") {
		return false
	}

	// Common command prefixes
	prefixes := []string{
		"find ", "grep ", "ls ", "cat ", "echo ", "cd ", "mkdir ", "rm ",
		"cp ", "mv ", "chmod ", "chown ", "docker ", "kubectl ", "git ",
		"npm ", "yarn ", "go ", "python ", "pip ", "curl ", "wget ",
		"python3 ",
		"jq ", "sed ", "awk ", "sort ", "uniq ", "head ", "tail ", "tar ",
		"sudo ", "./", "/",
	}

	lower := strings.ToLower(text)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	// Explanatory text patterns - if it has these, it's not a command
	if strings.Contains(text, ". ") || // Multiple sentences
		strings.HasPrefix(text, "The ") ||
		strings.HasPrefix(text, "This ") ||
		strings.HasPrefix(text, "To ") ||
		strings.HasPrefix(text, "You ") ||
		strings.HasPrefix(text, "I ") ||
		strings.HasPrefix(text, "I'") || // Contractions like "I'll", "I'm", "I've"
		strings.HasPrefix(text, "Here") ||
		strings.HasPrefix(text, "Let") ||
		strings.HasPrefix(text, "Looking") ||
		strings.Contains(text, " is ") ||
		strings.Contains(text, " are ") ||
		strings.Contains(text, " will ") ||
		strings.Contains(text, " can ") {
		return false
	}

	// Short single-line text without explanatory patterns might be a command
	if len(text) < 80 {
		return true
	}

	return false
}

// Close closes the HTTP client (no-op for HTTP).
func (t *HTTPTransport) Close() error {
	// HTTP is stateless, nothing to close
	return nil
}

// Compile-time check
var _ Transport = (*HTTPTransport)(nil)
