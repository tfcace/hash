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

// CurrentModel returns the configured model name.
func (t *HTTPTransport) CurrentModel() string {
	return t.config.Model
}

// AvailableModels returns nil: model enumeration is ACP-only.
func (t *HTTPTransport) AvailableModels() []ModelOption {
	return nil
}

// SetModel is unsupported for the HTTP transport; the model is fixed by config.
func (t *HTTPTransport) SetModel(ctx context.Context, value string) error {
	return fmt.Errorf("model selection is not supported for the http transport (set [agent] model in config)")
}

// EnsureModelInfo is a no-op for the HTTP transport.
func (t *HTTPTransport) EnsureModelInfo(ctx context.Context) error {
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
	text = unwrapMarkdownInline(strings.TrimSpace(text))
	if text == "" {
		return false
	}

	// Multi-line text is likely an explanation
	if strings.Contains(text, "\n") {
		return false
	}

	// Common command prefixes
	prefixes := []string{
		"find ", "grep ", "ls ", "cat ", "echo ", "cd ", "mkdir ", "rm ",
		"cp ", "mv ", "chmod ", "chown ", "docker ", "kubectl ", "git ",
		"npm ", "yarn ", "pnpm ", "bun ", "go ", "python ", "python3 ",
		"pip ", "pip3 ", "curl ", "wget ",
		"jq ", "sed ", "awk ", "sort ", "uniq ", "head ", "tail ", "tar ",
		"sudo ", "make ", "jj ", "hash ", "brew ", "helm ", "cargo ",
		"rustup ", "terraform ", "source ", "export ", "./", "../", "~/", "/",
	}

	lower := strings.ToLower(text)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	// Shell syntax strongly indicates executable text.
	if strings.Contains(text, " | ") ||
		strings.Contains(text, " > ") ||
		strings.Contains(text, " < ") ||
		strings.Contains(text, "&&") ||
		strings.Contains(text, "||") ||
		strings.Contains(text, ";") ||
		strings.HasSuffix(text, "&") ||
		strings.HasPrefix(text, "$(") {
		return true
	}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}

	// Leading env assignments are command-like when followed by a command.
	if len(fields) > 1 && isEnvAssignment(fields[0]) {
		return true
	}

	// Explanatory text patterns - if it has these, it's not a command
	if strings.Contains(text, ". ") || // Multiple sentences
		strings.HasPrefix(lower, "the ") ||
		strings.HasPrefix(lower, "this ") ||
		strings.HasPrefix(lower, "to ") ||
		strings.HasPrefix(lower, "you ") ||
		strings.HasPrefix(lower, "i ") ||
		strings.HasPrefix(lower, "i'") || // Contractions like "I'll", "I'm", "I've"
		strings.HasPrefix(lower, "here") ||
		strings.HasPrefix(lower, "let") ||
		strings.HasPrefix(lower, "looking") ||
		strings.Contains(lower, " is ") ||
		strings.Contains(lower, " are ") ||
		strings.Contains(lower, " will ") ||
		strings.Contains(lower, " can ") ||
		strings.Contains(lower, " should ") {
		return false
	}

	// Flag-based suggestions like "foo --bar baz" still look command-like.
	if len(fields) > 1 && strings.HasPrefix(fields[1], "-") {
		return true
	}

	return false
}

func unwrapMarkdownInline(text string) string {
	wrappers := [][2]string{
		{"**", "**"},
		{"__", "__"},
		{"`", "`"},
		{"*", "*"},
		{"_", "_"},
	}

	for {
		changed := false
		for _, wrapper := range wrappers {
			prefix, suffix := wrapper[0], wrapper[1]
			if len(text) < len(prefix)+len(suffix) ||
				!strings.HasPrefix(text, prefix) ||
				!strings.HasSuffix(text, suffix) {
				continue
			}
			text = strings.TrimSpace(text[len(prefix) : len(text)-len(suffix)])
			changed = true
			break
		}
		if !changed {
			return text
		}
	}
}

func isEnvAssignment(field string) bool {
	name, _, ok := strings.Cut(field, "=")
	if !ok || name == "" {
		return false
	}

	for i, r := range name {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// Close closes the HTTP client (no-op for HTTP).
func (t *HTTPTransport) Close() error {
	// HTTP is stateless, nothing to close
	return nil
}

// Compile-time check
var _ Transport = (*HTTPTransport)(nil)
