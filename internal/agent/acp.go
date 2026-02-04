package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ACPConfig configures the ACP transport.
type ACPConfig struct {
	Command string   // Command to execute (e.g., "claude-code-acp" or "gemini --experimental-acp")
	Args    []string // Additional arguments for the command
}

// ParsedCommand parses the Command string and returns the program and combined arguments.
// If Command contains spaces, it's split into program + args, which are prepended to Args.
// This allows users to write "gemini --experimental-acp" as a single command string.
func (c ACPConfig) ParsedCommand() (program string, args []string) {
	parts := strings.Fields(c.Command)
	if len(parts) == 0 {
		return c.Command, c.Args
	}

	program = parts[0]
	if len(parts) > 1 {
		// Command string has embedded args - combine with explicit args
		args = append(parts[1:], c.Args...)
	} else {
		args = c.Args
	}
	return program, args
}

// ACPTransport communicates with an ACP-compatible agent via JSON-RPC 2.0.
type ACPTransport struct {
	config    ACPConfig
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	reader    *bufio.Reader
	mu        sync.Mutex
	requestID atomic.Int64
	sessionID string

	// Channel for incoming messages
	messages chan []byte
	done     chan struct{}

	// Permission handler callback
	permissionHandler func(command string) (allow bool, always bool)
}

// JSON-RPC 2.0 message types
type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// ACP protocol types
type initializeParams struct {
	ProtocolVersion int                    `json:"protocolVersion"`
	ClientInfo      clientInfo             `json:"clientInfo"`
	Capabilities    map[string]interface{} `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type newSessionParams struct {
	Cwd        string        `json:"cwd"`
	McpServers []interface{} `json:"mcpServers"`
}

type newSessionResult struct {
	SessionID string `json:"sessionId"`
}

type promptParams struct {
	SessionID string       `json:"sessionId"`
	Prompt    []promptPart `json:"prompt"`
}

type promptPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type sessionUpdateParams struct {
	SessionID string        `json:"sessionId"`
	Update    sessionUpdate `json:"update"`
}

type sessionUpdate struct {
	SessionUpdate string        `json:"sessionUpdate"`
	Content       *contentBlock `json:"content,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// requestPermission types (agent -> client request)
type requestPermissionParams struct {
	SessionID string `json:"sessionId"`
	ToolCall  struct {
		ToolCallID string          `json:"toolCallId"`
		Title      string          `json:"title"`
		RawInput   json.RawMessage `json:"rawInput"`
	} `json:"toolCall"`
	Options []permissionOption `json:"options"`
}

type permissionOption struct {
	Kind     string `json:"kind"`     // "allow_once", "allow_always", "reject_once"
	Name     string `json:"name"`     // Display name
	OptionID string `json:"optionId"` // ID to return
}

type permissionResponse struct {
	Outcome *permissionOutcome `json:"outcome"`
}

type permissionOutcome struct {
	Outcome  string `json:"outcome"`  // "selected" or "canceled"
	OptionID string `json:"optionId"` // Which option was selected
}

// jsonRPCResponse is used to send responses to agent requests.
type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int64         `json:"id"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

// NewACPTransport creates a new ACP transport.
func NewACPTransport(cfg ACPConfig) *ACPTransport {
	return &ACPTransport{
		config:   cfg,
		messages: make(chan []byte, 1024),
		done:     make(chan struct{}),
	}
}

// SetPermissionHandler sets the callback for handling permission requests.
// The callback receives the command and returns (allow, always).
// If always is true, the command should be added to the allowlist.
func (t *ACPTransport) SetPermissionHandler(handler func(command string) (allow bool, always bool)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.permissionHandler = handler
}

// Name returns the transport name.
func (t *ACPTransport) Name() string {
	return "acp"
}

// Connect starts the agent process and initializes the ACP session.
func (t *ACPTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connectLocked(ctx)
}

func (t *ACPTransport) connectLocked(ctx context.Context) error {
	if t.stdin != nil {
		return nil // Already connected
	}

	program, args := t.config.ParsedCommand()
	t.cmd = exec.Command(program, args...)

	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	// Discard stderr to avoid blocking
	t.cmd.Stderr = nil

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	t.reader = bufio.NewReader(t.stdout)

	// Start reading messages in background
	go t.readLoop()

	// Initialize protocol
	if err := t.initialize(ctx); err != nil {
		t.Close() //nolint:errcheck // best-effort cleanup on init failure
		return fmt.Errorf("initialize: %w", err)
	}

	return nil
}

func (t *ACPTransport) readLoop() {
	defer close(t.done)
	defer close(t.messages)

	for {
		line, err := t.reader.ReadBytes('\n')
		if err != nil {
			return
		}
		// Send to channel; backpressure avoids dropping responses.
		t.messages <- line
	}
}

// sendCancel sends a session/cancel notification to stop ongoing operations.
// This is a notification (no response expected) per ACP spec.
// After cancel, the session is invalidated to force a fresh session on next request.
func (t *ACPTransport) sendCancel() {
	t.mu.Lock()
	defer t.mu.Unlock()

	sessionID := t.sessionID
	if sessionID == "" {
		return
	}

	// Invalidate the session - next request will create a fresh one
	t.sessionID = ""

	// Notifications don't have an ID field
	notification := struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params"`
	}{
		JSONRPC: "2.0",
		Method:  "session/cancel",
		Params: struct {
			SessionID string `json:"sessionId"`
		}{
			SessionID: sessionID,
		},
	}

	data, err := json.Marshal(notification)
	if err != nil {
		return // Best effort
	}

	_, _ = t.stdin.Write(append(data, '\n'))
}

func (t *ACPTransport) sendRequest(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := t.requestID.Add(1)

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if _, err := t.stdin.Write(append(data, '\n')); err != nil {
		// Write failed - pipe is likely broken (e.g., agent exited after cancel).
		// Reset connection so next Send() will reconnect via lazy connect.
		t.resetConnection()
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Wait for response with matching ID
	for {
		select {
		case <-ctx.Done():
			t.sendCancel()
			return nil, ctx.Err()
		case line, ok := <-t.messages:
			if !ok {
				// Connection closed (readLoop exited) - reset for reconnect
				t.resetConnection()
				return nil, fmt.Errorf("connection closed")
			}

			var msg struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      *int64          `json:"id"`
				Result  json.RawMessage `json:"result"`
				Error   *jsonRPCError   `json:"error"`
			}
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}

			// Check if this is our response
			if msg.ID != nil && *msg.ID == id {
				if msg.Error != nil {
					return nil, fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message)
				}
				return msg.Result, nil
			}
			// Not our response, continue waiting
		}
	}
}

func (t *ACPTransport) initialize(ctx context.Context) error {
	params := initializeParams{
		ProtocolVersion: 1,
		ClientInfo: clientInfo{
			Name:    "hash",
			Version: "0.1.0",
		},
		Capabilities: map[string]interface{}{},
	}

	_, err := t.sendRequest(ctx, "initialize", params)
	return err
}

func (t *ACPTransport) newSession(ctx context.Context, cwd string) (string, error) {
	params := newSessionParams{
		Cwd:        cwd,
		McpServers: []interface{}{},
	}

	result, err := t.sendRequest(ctx, "session/new", params)
	if err != nil {
		return "", err
	}

	var sessionResult newSessionResult
	if err := json.Unmarshal(result, &sessionResult); err != nil {
		return "", fmt.Errorf("parse session result: %w", err)
	}

	return sessionResult.SessionID, nil
}

// Send sends a request to the agent.
//
//nolint:gocyclo // SSE protocol parsing requires sequential event handling
func (t *ACPTransport) Send(ctx context.Context, req Request) (<-chan Response, error) {
	t.mu.Lock()

	// Lazy connect
	if t.stdin == nil {
		if err := t.connectLocked(ctx); err != nil {
			t.mu.Unlock()
			return nil, fmt.Errorf("connect: %w", err)
		}
	}

	// Check if we need a new session
	needSession := t.sessionID == ""
	t.mu.Unlock()

	// Create session if needed (outside lock to avoid deadlock on timeout)
	if needSession {
		cwd := req.Context.Cwd
		if cwd == "" {
			cwd = "."
		}
		sessionID, err := t.newSession(ctx, cwd)
		if err != nil {
			return nil, fmt.Errorf("new session: %w", err)
		}
		t.mu.Lock()
		t.sessionID = sessionID
		t.mu.Unlock()
	}

	t.mu.Lock()
	sessionID := t.sessionID
	t.mu.Unlock()

	respCh := make(chan Response, 1)

	go func() {
		defer close(respCh)

		// Build prompt with context
		promptText := buildPromptWithContext(req)

		// Send prompt request
		id := t.requestID.Add(1)
		rpcReq := jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "session/prompt",
			Params: promptParams{
				SessionID: sessionID,
				Prompt: []promptPart{
					{Type: "text", Text: promptText},
				},
			},
		}

		data, err := json.Marshal(rpcReq)
		if err != nil {
			respCh <- Response{Type: ResponseTypeError, Error: err.Error()}
			return
		}

		t.mu.Lock()
		_, err = t.stdin.Write(append(data, '\n'))
		t.mu.Unlock()
		if err != nil {
			// Write failed - reset connection so next Send() reconnects
			t.resetConnection()
			respCh <- Response{Type: ResponseTypeError, Error: err.Error()}
			return
		}

		// Collect response text from streaming notifications
		var textBuilder strings.Builder

		// Create idle timer
		idleTimer := time.NewTimer(IdleTimeout)
		defer idleTimer.Stop()

		for {
			select {
			case <-ctx.Done():
				t.sendCancel()
				respCh <- Response{Type: ResponseTypeError, Error: ctx.Err().Error()}
				return

			case <-idleTimer.C:
				// No message received for IdleTimeout - agent may be stuck
				t.sendCancel()
				text := textBuilder.String()
				if text != "" {
					// Return partial response with warning
					respCh <- parseAgentResponse(text)
				} else {
					respCh <- Response{Type: ResponseTypeError, Error: fmt.Sprintf("agent idle timeout (%v without response)", IdleTimeout)}
				}
				return

			case line, ok := <-t.messages:
				if !ok {
					// Connection closed (readLoop exited) - reset for reconnect
					t.resetConnection()
					text := textBuilder.String()
					if text != "" {
						respCh <- parseAgentResponse(text)
					} else {
						respCh <- Response{Type: ResponseTypeError, Error: "connection closed"}
					}
					return
				}

				// Reset idle timer on any message
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(IdleTimeout)

				var msg struct {
					JSONRPC string          `json:"jsonrpc"`
					ID      *int64          `json:"id"`
					Method  string          `json:"method"`
					Params  json.RawMessage `json:"params"`
					Result  json.RawMessage `json:"result"`
					Error   *jsonRPCError   `json:"error"`
				}
				if err := json.Unmarshal(line, &msg); err != nil {
					continue
				}

				// Check if this is an incoming request (has both ID and Method)
				if msg.ID != nil && msg.Method != "" {
					go t.handleIncomingRequest(*msg.ID, msg.Method, msg.Params)
					continue
				}

				// Check if it's our response (end of prompt)
				if msg.ID != nil && *msg.ID == id {
					if msg.Error != nil {
						respCh <- Response{Type: ResponseTypeError, Error: msg.Error.Message}
						return
					}
					// Prompt complete
					text := textBuilder.String()
					if text != "" {
						respCh <- parseAgentResponse(text)
					} else {
						respCh <- Response{Type: ResponseTypeError, Error: "agent returned empty response"}
					}
					return
				}

				// Handle session/update notification
				if msg.Method == "session/update" {
					var updateParams sessionUpdateParams
					if err := json.Unmarshal(msg.Params, &updateParams); err != nil {
						continue
					}

					if updateParams.Update.SessionUpdate == "agent_message_chunk" &&
						updateParams.Update.Content != nil &&
						updateParams.Update.Content.Type == "text" {
						textBuilder.WriteString(updateParams.Update.Content.Text)
					}
				}
			}
		}
	}()

	return respCh, nil
}

func parseAgentResponse(text string) Response {
	text = strings.TrimSpace(text)

	// Simple heuristic: if it looks like a command, return as command
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

// handleIncomingRequest processes requests from the agent (like session/request_permission).
func (t *ACPTransport) handleIncomingRequest(id int64, method string, params json.RawMessage) {
	switch method {
	case "session/request_permission":
		t.handleRequestPermission(id, params)
	default:
		// Unknown method - send error response
		t.sendResponse(id, nil, &jsonRPCError{
			Code:    -32601,
			Message: "Method not found",
		})
	}
}

// handleRequestPermission handles permission requests from the agent.
func (t *ACPTransport) handleRequestPermission(id int64, params json.RawMessage) {
	var p requestPermissionParams
	if err := json.Unmarshal(params, &p); err != nil {
		t.sendResponse(id, nil, &jsonRPCError{
			Code:    -32602,
			Message: "Invalid params",
		})
		return
	}

	// Extract command from tool call title or raw input
	command := p.ToolCall.Title
	if command == "" {
		// Try to extract from rawInput (usually has "command" field for Bash)
		var input struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(p.ToolCall.RawInput, &input); err == nil && input.Command != "" {
			command = input.Command
		}
	}

	// Call the permission handler
	t.mu.Lock()
	handler := t.permissionHandler
	t.mu.Unlock()

	var outcome permissionOutcome
	if handler == nil {
		// No handler - deny by default
		outcome = permissionOutcome{Outcome: "selected", OptionID: "reject"}
	} else {
		allow, always := handler(command)
		if allow {
			if always {
				outcome = permissionOutcome{Outcome: "selected", OptionID: "allow_always"}
			} else {
				outcome = permissionOutcome{Outcome: "selected", OptionID: "allow"}
			}
		} else {
			outcome = permissionOutcome{Outcome: "selected", OptionID: "reject"}
		}
	}

	t.sendResponse(id, permissionResponse{Outcome: &outcome}, nil)
}

// sendResponse sends a JSON-RPC response.
func (t *ACPTransport) sendResponse(id int64, result interface{}, err *jsonRPCError) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
		Error:   err,
	}

	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stdin != nil {
		t.stdin.Write(append(data, '\n'))
	}
}

// buildPromptWithContext builds a prompt that includes context information.
func buildPromptWithContext(req Request) string {
	var b strings.Builder

	// Add conversation marker instruction
	b.WriteString(`For multi-turn conversations (questions, offering options, asking for clarification):
- Start with [CONVERSATION] on the first line
- End with [AWAITING_INPUT] on the last line
Do NOT use these markers for complete answers, commands, or when the conversation naturally concludes.

`)

	ctx := req.Context

	// Build context section
	var contextLines strings.Builder
	if ctx.Cwd != "" {
		fmt.Fprintf(&contextLines, "- Working directory: %s\n", ctx.Cwd)
	}
	if ctx.GitBranch != "" {
		fmt.Fprintf(&contextLines, "- Git branch: %s\n", ctx.GitBranch)
	}
	if ctx.KubeContext != "" {
		fmt.Fprintf(&contextLines, "- Kubernetes context: %s\n", ctx.KubeContext)
	}
	if len(ctx.History) > 0 {
		contextLines.WriteString("- Recent commands:\n")
		for _, cmd := range ctx.History {
			fmt.Fprintf(&contextLines, "  - %s\n", cmd)
		}
	}
	if len(ctx.EnvVars) > 0 {
		contextLines.WriteString("- Environment:\n")
		for k, v := range ctx.EnvVars {
			fmt.Fprintf(&contextLines, "  - %s=%s\n", k, v)
		}
	}
	if ctx.LastOutput != "" {
		fmt.Fprintf(&contextLines, "- Last command output:\n%s\n", ctx.LastOutput)
	}
	if ctx.LastError != "" {
		fmt.Fprintf(&contextLines, "- Last error:\n%s\n", ctx.LastError)
	}

	// Only add context header if there's content
	if contextLines.Len() > 0 {
		b.WriteString("Context:\n")
		b.WriteString(contextLines.String())
		b.WriteString("\nUser request: ")
	}
	b.WriteString(req.Prompt)

	return b.String()
}

// resetConnection closes the current connection and resets state so that
// the next Send() will reconnect via lazy connect. This is called when
// a write error occurs (e.g., broken pipe after agent exits).
func (t *ACPTransport) resetConnection() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stdin != nil {
		t.stdin.Close()
		t.stdin = nil
	}
	if t.stdout != nil {
		t.stdout.Close()
		t.stdout = nil
	}
	t.reader = nil
	t.sessionID = ""
	// Note: We don't kill cmd here - it may have already exited.
	// The next connectLocked() will start a fresh process.
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill() //nolint:errcheck // best-effort kill
		t.cmd.Wait()         //nolint:errcheck // ignore exit status
		t.cmd = nil
	}
}

// Close terminates the agent process.
func (t *ACPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stdin != nil {
		t.stdin.Close()
		t.stdin = nil
	}
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill() //nolint:errcheck // best-effort kill during cleanup
		t.cmd.Wait()         //nolint:errcheck // ignore exit status during cleanup
	}
	return nil
}

// IdleTimeout is the maximum time to wait without receiving any message from the agent.
// If exceeded, the request is considered stuck and canceled.
const IdleTimeout = 30 * time.Second

// SendStreaming implements StreamingTransport for real-time text streaming.
//
//nolint:gocritic,gocyclo // unnamedResult + SSE streaming requires sequential event handling
func (t *ACPTransport) SendStreaming(ctx context.Context, req Request) (<-chan string, <-chan error) {
	textCh := make(chan string, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(textCh)
		defer close(errCh)

		t.mu.Lock()

		// Lazy connect
		if t.stdin == nil {
			if err := t.connectLocked(ctx); err != nil {
				t.mu.Unlock()
				errCh <- err
				return
			}
		}

		// Check if we need a new session
		needSession := t.sessionID == ""
		t.mu.Unlock()

		// Create session if needed (outside lock to avoid deadlock on timeout)
		if needSession {
			cwd := req.Context.Cwd
			if cwd == "" {
				cwd = "."
			}
			sessionID, err := t.newSession(ctx, cwd)
			if err != nil {
				errCh <- err
				return
			}
			t.mu.Lock()
			t.sessionID = sessionID
			t.mu.Unlock()
		}

		t.mu.Lock()
		sessionID := t.sessionID
		t.mu.Unlock()

		// Build prompt with context
		promptText := buildPromptWithContext(req)

		// Send prompt request
		id := t.requestID.Add(1)
		rpcReq := jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "session/prompt",
			Params: promptParams{
				SessionID: sessionID,
				Prompt: []promptPart{
					{Type: "text", Text: promptText},
				},
			},
		}

		data, err := json.Marshal(rpcReq)
		if err != nil {
			errCh <- err
			return
		}

		t.mu.Lock()
		_, err = t.stdin.Write(append(data, '\n'))
		t.mu.Unlock()
		if err != nil {
			// Write failed - reset connection so next Send() reconnects
			t.resetConnection()
			errCh <- err
			return
		}

		// Create idle timer
		idleTimer := time.NewTimer(IdleTimeout)
		defer idleTimer.Stop()

		// Stream response text as it arrives
		for {
			select {
			case <-ctx.Done():
				t.sendCancel()
				errCh <- ctx.Err()
				return

			case <-idleTimer.C:
				// No message received for IdleTimeout - agent may be stuck
				t.sendCancel()
				errCh <- fmt.Errorf("agent idle timeout (%v without response)", IdleTimeout)
				return

			case line, ok := <-t.messages:
				if !ok {
					// Connection closed (readLoop exited) - reset for reconnect
					t.resetConnection()
					return
				}

				// Reset idle timer on any message
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(IdleTimeout)

				var msg struct {
					JSONRPC string          `json:"jsonrpc"`
					ID      *int64          `json:"id"`
					Method  string          `json:"method"`
					Params  json.RawMessage `json:"params"`
					Result  json.RawMessage `json:"result"`
					Error   *jsonRPCError   `json:"error"`
				}
				if err := json.Unmarshal(line, &msg); err != nil {
					continue
				}

				// Check if this is an incoming request (has both ID and Method)
				if msg.ID != nil && msg.Method != "" {
					go t.handleIncomingRequest(*msg.ID, msg.Method, msg.Params)
					continue
				}

				// Check if it's our response (end of prompt)
				if msg.ID != nil && *msg.ID == id {
					if msg.Error != nil {
						errCh <- fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message)
					}
					return
				}

				// Handle session/update notification - stream text chunks
				if msg.Method == "session/update" {
					var updateParams sessionUpdateParams
					if err := json.Unmarshal(msg.Params, &updateParams); err != nil {
						continue
					}

					if updateParams.Update.SessionUpdate == "agent_message_chunk" &&
						updateParams.Update.Content != nil &&
						updateParams.Update.Content.Type == "text" {
						// Send text chunk immediately
						textCh <- updateParams.Update.Content.Text
					}
				}
			}
		}
	}()

	return textCh, errCh
}

// Compile-time checks
var _ Transport = (*ACPTransport)(nil)
var _ StreamingTransport = (*ACPTransport)(nil)
