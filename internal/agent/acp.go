package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ACPConfig configures the ACP transport.
type ACPConfig struct {
	Command string   // Command to execute (e.g., "claude-agent-acp" or "gemini --experimental-acp")
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
		args = append(args, parts[1:]...)
		args = append(args, c.Args...)
	} else {
		args = c.Args
	}
	return program, args
}

// ToolPermissionRequest contains details about a tool call that requires permission.
type ToolPermissionRequest struct {
	Command    string // The command or operation to execute
	ToolName   string // Tool name/type (e.g., "Bash", "Read", "Write")
	ToolCallID string // Unique tool call ID
}

// ACPTransport communicates with an ACP-compatible agent via JSON-RPC 2.0.
type ACPTransport struct {
	config     ACPConfig
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	reader     *bufio.Reader
	mu         sync.Mutex
	requestID  atomic.Int64
	sessionID  string
	sessionCWD string

	capabilities     acpCapabilities
	resumeSessionID  string
	resumeSessionCWD string

	// Channel for incoming messages
	messages chan []byte
	done     chan struct{}

	// Permission handler callback
	permissionHandler  func(req ToolPermissionRequest) (allow bool, always bool)
	permissionPromptMu sync.Mutex
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
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientInfo         clientInfo         `json:"clientInfo"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type clientCapabilities struct {
	FS       fileSystemCapabilities `json:"fs"`
	Terminal bool                   `json:"terminal"`
}

type fileSystemCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type initializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities agentCapabilities `json:"agentCapabilities"`
	AgentInfo         agentInfo         `json:"agentInfo"`
}

type agentInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type agentCapabilities struct {
	LoadSession         bool                `json:"loadSession"`
	SessionCapabilities sessionCapabilities `json:"sessionCapabilities"`
}

type sessionCapabilities struct {
	Close  json.RawMessage `json:"close,omitempty"`
	Fork   json.RawMessage `json:"fork,omitempty"`
	List   json.RawMessage `json:"list,omitempty"`
	Resume json.RawMessage `json:"resume,omitempty"`
}

type acpCapabilities struct {
	LoadSession   bool
	SessionClose  bool
	SessionFork   bool
	SessionList   bool
	SessionResume bool
}

func (c agentCapabilities) normalized() acpCapabilities {
	return acpCapabilities{
		LoadSession:   c.LoadSession,
		SessionClose:  capabilityObjectSet(c.SessionCapabilities.Close),
		SessionFork:   capabilityObjectSet(c.SessionCapabilities.Fork),
		SessionList:   capabilityObjectSet(c.SessionCapabilities.List),
		SessionResume: capabilityObjectSet(c.SessionCapabilities.Resume),
	}
}

func capabilityObjectSet(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

type newSessionParams struct {
	Cwd        string        `json:"cwd"`
	McpServers []interface{} `json:"mcpServers"`
}

type newSessionResult struct {
	SessionID string `json:"sessionId"`
}

type resumeSessionParams struct {
	SessionID  string        `json:"sessionId"`
	Cwd        string        `json:"cwd"`
	McpServers []interface{} `json:"mcpServers"`
}

type closeSessionParams struct {
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
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content,omitempty"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	Status        string          `json:"status,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type toolCallContent struct {
	Type    string        `json:"type"`
	Text    string        `json:"text,omitempty"`
	Content *contentBlock `json:"content,omitempty"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
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
// The callback receives a ToolPermissionRequest and returns (allow, always).
// If always is true, the command should be added to the allowlist.
func (t *ACPTransport) SetPermissionHandler(handler func(req ToolPermissionRequest) (allow bool, always bool)) {
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
		return errors.Join(ErrACPStartFailed, fmt.Errorf("stdin pipe: %w", err))
	}

	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		t.stdin.Close()
		t.stdin = nil
		return errors.Join(ErrACPStartFailed, fmt.Errorf("stdout pipe: %w", err))
	}

	// Discard stderr to avoid blocking
	t.cmd.Stderr = nil

	if err = t.cmd.Start(); err != nil {
		t.stdin.Close()
		t.stdin = nil
		t.stdout = nil // stdout pipe is already invalidated when cmd fails to start
		return errors.Join(ErrACPStartFailed, fmt.Errorf("start agent: %w", err))
	}

	t.reader = bufio.NewReader(t.stdout)

	// Start reading messages in background
	go t.readLoop(t.reader, t.messages, t.done)

	// Release the lock before initialize — it calls sendRequest which may
	// call sendCancel or resetConnection, both of which acquire mu.
	t.mu.Unlock()
	err = t.initialize(ctx)
	t.mu.Lock()

	if err != nil {
		t.resetConnectionLocked()
		return fmt.Errorf("initialize: %w", err)
	}

	return nil
}

func (t *ACPTransport) readLoop(reader *bufio.Reader, messages chan []byte, done chan struct{}) {
	defer close(done)
	defer close(messages)

	if reader == nil {
		return
	}

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		// Send to channel; backpressure avoids dropping responses.
		messages <- line
	}
}

const acpSessionCloseTimeout = 1500 * time.Millisecond

func (t *ACPTransport) takeCurrentSessionForShutdown() (sessionID string, canClose bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	sessionID = t.sessionID
	if sessionID == "" {
		return "", false
	}

	canClose = t.capabilities.SessionClose && t.stdin != nil
	t.sessionID = ""
	t.sessionCWD = ""
	return sessionID, canClose
}

func (t *ACPTransport) sendCancelNotification(sessionID string) {
	// Notifications don't have an ID field.
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
		return // Best effort.
	}

	t.mu.Lock()
	stdin := t.stdin
	t.mu.Unlock()
	if stdin != nil {
		_, _ = stdin.Write(append(data, '\n'))
	}
}

// sendCancel stops ongoing session work. Agents with session/close support get
// a close request; older agents get the baseline session/cancel notification.
// After cancel, the session is invalidated to force a fresh session on next request.
func (t *ACPTransport) sendCancel() {
	sessionID, canClose := t.takeCurrentSessionForShutdown()
	if sessionID == "" {
		return
	}

	if canClose {
		ctx, cancel := context.WithTimeout(context.Background(), acpSessionCloseTimeout)
		err := t.closeSession(ctx, sessionID)
		cancel()
		if err == nil {
			return
		}
		t.resetConnection()
		return
	}

	t.sendCancelNotification(sessionID)
}

func (t *ACPTransport) closeCurrentSession() {
	t.sendCancel()
}

func (t *ACPTransport) closeSession(ctx context.Context, sessionID string) error {
	_, err := t.sendRequestWithoutCancel(ctx, "session/close", closeSessionParams{
		SessionID: sessionID,
	})
	return err
}

func (t *ACPTransport) sendRequest(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	return t.sendRequestWithOptions(ctx, method, params, true)
}

func (t *ACPTransport) sendRequestWithoutCancel(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	return t.sendRequestWithOptions(ctx, method, params, false)
}

func (t *ACPTransport) sendRequestWithOptions(ctx context.Context, method string, params interface{}, cancelOnContext bool) (json.RawMessage, error) {
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
		return nil, errors.Join(ErrACPConnectionClosed, fmt.Errorf("write request: %w", err))
	}

	// Wait for response with matching ID
	for {
		select {
		case <-ctx.Done():
			if cancelOnContext {
				t.sendCancel()
			}
			return nil, ctx.Err()
		case line, ok := <-t.messages:
			if !ok {
				// Connection closed (readLoop exited) - reset for reconnect
				t.resetConnection()
				return nil, fmt.Errorf("%w: connection closed", ErrACPConnectionClosed)
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
			Title:   "Hash",
			Version: "0.1.0",
		},
		ClientCapabilities: clientCapabilities{
			FS: fileSystemCapabilities{
				ReadTextFile:  false,
				WriteTextFile: false,
			},
			Terminal: false,
		},
	}

	result, err := t.sendRequest(ctx, "initialize", params)
	if err != nil {
		return err
	}

	var initResult initializeResult
	if err := json.Unmarshal(result, &initResult); err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}
	if initResult.ProtocolVersion != params.ProtocolVersion {
		return fmt.Errorf("unsupported ACP protocol version %d (hash supports %d)",
			initResult.ProtocolVersion,
			params.ProtocolVersion,
		)
	}
	if err := rejectKnownUnsupportedAgent(initResult.AgentInfo); err != nil {
		return err
	}
	t.mu.Lock()
	t.capabilities = initResult.AgentCapabilities.normalized()
	t.mu.Unlock()
	return nil
}

func rejectKnownUnsupportedAgent(info agentInfo) error {
	if info.Name != "@zed-industries/claude-code-acp" {
		return nil
	}

	version := strings.TrimSpace(info.Version)
	if version == "" {
		version = "unknown"
	}
	return errors.Join(
		ErrACPUnsupportedAgent,
		fmt.Errorf("configured ACP agent %s %s is deprecated and can finish prompts without emitting messages; install @agentclientprotocol/claude-agent-acp and set [agent] command = \"claude-agent-acp\"",
			info.Name,
			version,
		),
	)
}

func (t *ACPTransport) newSession(ctx context.Context, cwd string) (string, error) {
	params := newSessionParams{
		Cwd:        normalizeSessionCWD(cwd),
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

func (t *ACPTransport) resumeSession(ctx context.Context, sessionID, cwd string) error {
	params := resumeSessionParams{
		SessionID:  sessionID,
		Cwd:        normalizeSessionCWD(cwd),
		McpServers: []interface{}{},
	}

	_, err := t.sendRequest(ctx, "session/resume", params)
	return err
}

func (t *ACPTransport) ensureSession(ctx context.Context, cwd string) (string, error) {
	normalizedCWD := normalizeSessionCWD(cwd)

	t.mu.Lock()
	if t.sessionID != "" {
		sessionID := t.sessionID
		t.mu.Unlock()
		return sessionID, nil
	}
	resumeID := t.resumeSessionID
	resumeCWD := t.resumeSessionCWD
	canResume := t.capabilities.SessionResume
	t.mu.Unlock()

	if canResume && resumeID != "" {
		if resumeCWD == "" {
			resumeCWD = normalizedCWD
		}
		if err := t.resumeSession(ctx, resumeID, resumeCWD); err == nil {
			t.mu.Lock()
			t.sessionID = resumeID
			t.sessionCWD = resumeCWD
			t.resumeSessionID = ""
			t.resumeSessionCWD = ""
			t.mu.Unlock()
			return resumeID, nil
		} else if IsRetryableError(err) {
			return "", err
		}

		t.mu.Lock()
		if t.resumeSessionID == resumeID {
			t.resumeSessionID = ""
			t.resumeSessionCWD = ""
		}
		t.mu.Unlock()
	}

	newSessionID, err := t.newSession(ctx, normalizedCWD)
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	t.sessionID = newSessionID
	t.sessionCWD = normalizedCWD
	t.resumeSessionID = ""
	t.resumeSessionCWD = ""
	t.mu.Unlock()
	return newSessionID, nil
}

func normalizeSessionCWD(cwd string) string {
	if cwd == "" {
		if current, err := os.Getwd(); err == nil {
			return current
		}
		return cwd
	}
	if filepath.IsAbs(cwd) {
		return cwd
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return abs
}

func parseAgentResponse(text string) Response {
	text = strings.TrimSpace(text)
	if text == "" {
		return Response{
			Type:  ResponseTypeError,
			Error: "empty response",
		}
	}

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

func agentMessageChunkText(update sessionUpdate) (string, bool) {
	if update.SessionUpdate != "agent_message_chunk" {
		return "", false
	}
	return textFromContentBlock(update.Content)
}

func toolCallUpdateText(update sessionUpdate) (string, bool) {
	switch update.SessionUpdate {
	case "tool_call", "tool_call_update":
	default:
		return "", false
	}
	return textFromToolCallContent(update.Content)
}

func textFromContentBlock(raw json.RawMessage) (string, bool) {
	if rawIsEmpty(raw) {
		return "", false
	}

	var block contentBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return "", false
	}
	if block.Type != "text" || block.Text == "" {
		return "", false
	}
	return block.Text, true
}

func textFromToolCallContent(raw json.RawMessage) (string, bool) {
	if rawIsEmpty(raw) {
		return "", false
	}

	var items []toolCallContent
	if err := json.Unmarshal(raw, &items); err == nil {
		return joinToolCallText(items)
	}

	var item toolCallContent
	if err := json.Unmarshal(raw, &item); err != nil {
		return "", false
	}
	return joinToolCallText([]toolCallContent{item})
}

func joinToolCallText(items []toolCallContent) (string, bool) {
	var b strings.Builder
	for _, item := range items {
		text, ok := textFromToolCallContentItem(item)
		if !ok {
			continue
		}
		appendTextPart(&b, text)
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", false
	}
	return b.String(), true
}

func textFromToolCallContentItem(item toolCallContent) (string, bool) {
	switch item.Type {
	case "content":
		if item.Content == nil || item.Content.Type != "text" || item.Content.Text == "" {
			return "", false
		}
		return item.Content.Text, true
	case "text":
		if item.Text == "" {
			return "", false
		}
		return item.Text, true
	default:
		return "", false
	}
}

func appendTextPart(b *strings.Builder, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(text)
}

func rawIsEmpty(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

func noOutputPromptError(result json.RawMessage, toolUpdateCount int) error {
	stopReason := promptStopReason(result)
	detail := fmt.Sprintf("prompt completed without displayable text (stopReason=%s", stopReason)
	if toolUpdateCount > 0 {
		detail += fmt.Sprintf(", toolUpdates=%d", toolUpdateCount)
	}
	detail += ")"
	return errors.Join(ErrACPNoOutput, errors.New(detail))
}

func promptStopReason(result json.RawMessage) string {
	if rawIsEmpty(result) {
		return "unknown"
	}
	var parsedPromptResult promptResult
	if err := json.Unmarshal(result, &parsedPromptResult); err != nil || parsedPromptResult.StopReason == "" {
		return "unknown"
	}
	return parsedPromptResult.StopReason
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

	// Extract tool name from rawInput if available
	toolName := extractToolName(p.ToolCall.RawInput)

	// Reject if we can't determine what command the agent wants to run
	if command == "" {
		t.sendResponse(id, permissionResponse{
			Outcome: &permissionOutcome{
				Outcome:  "selected",
				OptionID: resolveOptionID(p.Options, "reject_once", "reject"),
			},
		}, nil)
		return
	}

	// Call the permission handler
	t.mu.Lock()
	handler := t.permissionHandler
	t.mu.Unlock()

	req := ToolPermissionRequest{
		Command:    command,
		ToolName:   toolName,
		ToolCallID: p.ToolCall.ToolCallID,
	}

	var outcome permissionOutcome
	if handler == nil {
		// No handler - deny by default
		outcome = permissionOutcome{
			Outcome:  "selected",
			OptionID: resolveOptionID(p.Options, "reject_once", "reject"),
		}
	} else {
		t.permissionPromptMu.Lock()
		allow, always := handler(req)
		t.permissionPromptMu.Unlock()
		if allow {
			if always {
				outcome = permissionOutcome{
					Outcome:  "selected",
					OptionID: resolveOptionID(p.Options, "allow_always", "allow_always"),
				}
			} else {
				outcome = permissionOutcome{
					Outcome:  "selected",
					OptionID: resolveOptionID(p.Options, "allow_once", "allow"),
				}
			}
		} else {
			outcome = permissionOutcome{
				Outcome:  "selected",
				OptionID: resolveOptionID(p.Options, "reject_once", "reject"),
			}
		}
	}

	t.sendResponse(id, permissionResponse{Outcome: &outcome}, nil)
}

// resolveOptionID finds the optionId for the given kind from the agent's
// provided options. If no options were provided or no match is found, falls
// back to the given default ID for backwards compatibility.
func resolveOptionID(options []permissionOption, kind, fallback string) string {
	for _, opt := range options {
		if opt.Kind == kind {
			return opt.OptionID
		}
	}
	return fallback
}

// extractToolName attempts to extract a tool name from the rawInput JSON.
// Agents typically include a "tool" or "name" field. Returns empty string
// if no tool name can be determined.
func extractToolName(rawInput json.RawMessage) string {
	if len(rawInput) == 0 {
		return ""
	}
	var fields struct {
		Tool string `json:"tool"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rawInput, &fields); err != nil {
		return ""
	}
	if fields.Tool != "" {
		return fields.Tool
	}
	return fields.Name
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

	b.WriteString("Be concise. Don't repeat information. No preamble.\n\n")

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
// the next SendStreaming() will reconnect via lazy connect. This is called when
// a write error occurs (e.g., broken pipe after agent exits).
func (t *ACPTransport) resetConnection() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resetConnectionLocked()
}

func (t *ACPTransport) rememberResumeCandidateLocked() {
	if t.capabilities.SessionResume && t.sessionID != "" {
		t.resumeSessionID = t.sessionID
		t.resumeSessionCWD = t.sessionCWD
	}
}

// resetConnectionLocked closes the current connection and resets state.
// Must be called with mu held.
func (t *ACPTransport) resetConnectionLocked() {
	t.rememberResumeCandidateLocked()
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
	t.sessionCWD = ""
	t.capabilities = acpCapabilities{}
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill() //nolint:errcheck // best-effort kill
		t.cmd.Wait()         //nolint:errcheck // ignore exit status
		t.cmd = nil
	}
	// Recreate channels so the next connectLocked()/readLoop() cycle
	// doesn't close already-closed channels (panic on double close).
	t.messages = make(chan []byte, 1024)
	t.done = make(chan struct{})
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
	t.sessionID = ""
	t.sessionCWD = ""
	t.resumeSessionID = ""
	t.resumeSessionCWD = ""
	t.capabilities = acpCapabilities{}
	return nil
}

// IdleTimeout is the maximum time to wait without receiving any message from the agent.
// If exceeded, the request is considered stuck and canceled.
const IdleTimeout = 30 * time.Second

func idleTimeoutForContext(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > IdleTimeout {
			return remaining
		}
	}
	return IdleTimeout
}

// SendStreaming implements Transport for real-time text streaming.
//
//nolint:gocritic,gocyclo // unnamedResult + SSE streaming requires sequential event handling
func (t *ACPTransport) SendStreaming(ctx context.Context, req Request) (<-chan string, <-chan error) {
	textCh := make(chan string, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(textCh)
		defer close(errCh)

		const maxAttempts = 2 // Initial try + one transparent reconnect retry
		for attempt := 0; attempt < maxAttempts; attempt++ {
			receivedText, err := t.sendStreamingAttempt(ctx, req, textCh)
			if err == nil {
				return
			}

			// Respect cancellation immediately; do not convert into transport errors.
			if ctx.Err() != nil {
				errCh <- ctx.Err()
				return
			}

			// Retry once for connection-level failures before any output is emitted.
			if attempt < maxAttempts-1 && !receivedText && IsRetryableError(err) {
				t.resetConnection()
				continue
			}

			errCh <- err
			return
		}
	}()

	return textCh, errCh
}

// sendStreamingAttempt performs one ACP streaming attempt.
// Returns whether any text chunks were emitted before completion/failure.
func (t *ACPTransport) sendStreamingAttempt( //nolint:gocyclo // streaming protocol handler with multiple message types
	ctx context.Context,
	req Request,
	textCh chan<- string,
) (receivedText bool, retErr error) {
	t.mu.Lock()

	// Lazy connect
	if t.stdin == nil {
		if connectErr := t.connectLocked(ctx); connectErr != nil {
			t.mu.Unlock()
			return false, connectErr
		}
	}

	t.mu.Unlock()

	sessionID, sessionErr := t.ensureSession(ctx, req.Context.Cwd)
	if sessionErr != nil {
		return false, sessionErr
	}

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
		return false, err
	}

	t.mu.Lock()
	_, err = t.stdin.Write(append(data, '\n'))
	t.mu.Unlock()
	if err != nil {
		// Write failed - reset connection so retry can lazily reconnect.
		t.resetConnection()
		return false, errors.Join(ErrACPConnectionClosed, fmt.Errorf("write prompt: %w", err))
	}

	idleTimeout := idleTimeoutForContext(ctx)
	var permissionRequestsInFlight atomic.Int32
	// Create idle timer
	idleTimer := time.NewTimer(idleTimeout)
	defer idleTimer.Stop()

	toolContentByID := make(map[string]string)
	var toolContentOrder []string
	toolUpdateCount := 0
	agentTextAfterLatestTool := true
	rememberToolContent := func(update sessionUpdate, text string) {
		toolCallID := update.ToolCallID
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("__unkeyed_tool_%d", len(toolContentOrder)+1)
		}
		if _, exists := toolContentByID[toolCallID]; !exists {
			toolContentOrder = append(toolContentOrder, toolCallID)
		}
		toolContentByID[toolCallID] = text
		agentTextAfterLatestTool = false
	}
	toolFallbackText := func() string {
		var b strings.Builder
		for _, toolCallID := range toolContentOrder {
			appendTextPart(&b, toolContentByID[toolCallID])
		}
		return b.String()
	}
	emitToolFallback := func() bool {
		if agentTextAfterLatestTool {
			return false
		}
		text := toolFallbackText()
		if strings.TrimSpace(text) == "" {
			return false
		}
		if receivedText {
			textCh <- "\n"
		}
		textCh <- text
		receivedText = true
		agentTextAfterLatestTool = true
		return true
	}

	// Stream response text as it arrives
	for {
		select {
		case <-ctx.Done():
			t.sendCancel()
			return receivedText, ctx.Err()

		case <-idleTimer.C:
			// Don't timeout while we're waiting on a local permission prompt.
			// The agent is blocked on our response, so this is user think-time,
			// not transport idleness.
			if permissionRequestsInFlight.Load() > 0 {
				idleTimer.Reset(idleTimeout)
				continue
			}

			// No message received for IdleTimeout - agent may be stuck.
			t.sendCancel()
			// Force reconnect after idle timeout: the process may be hung.
			t.resetConnection()
			return receivedText, errors.Join(
				ErrACPIdleTimeout,
				fmt.Errorf("agent idle timeout (%v without response)", idleTimeout),
			)

		case line, ok := <-t.messages:
			if !ok {
				// Connection closed (readLoop exited) - reset for reconnect.
				t.resetConnection()
				return receivedText, fmt.Errorf("%w: connection closed", ErrACPConnectionClosed)
			}

			// Reset idle timer on any message.
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idleTimeout)

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

			// Check if this is an incoming request (has both ID and Method).
			if msg.ID != nil && msg.Method != "" {
				permissionRequestsInFlight.Add(1)
				go func(id int64, method string, params json.RawMessage) {
					defer permissionRequestsInFlight.Add(-1)
					t.handleIncomingRequest(id, method, params)
				}(*msg.ID, msg.Method, msg.Params)
				continue
			}

			// Check if it's our response (end of prompt).
			if msg.ID != nil && *msg.ID == id {
				if msg.Error != nil {
					return receivedText, fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message)
				}
				emitToolFallback()
				if !receivedText {
					return false, noOutputPromptError(msg.Result, toolUpdateCount)
				}
				return receivedText, nil
			}

			// Handle session/update notification - stream text chunks.
			if msg.Method == "session/update" {
				var updateParams sessionUpdateParams
				if err := json.Unmarshal(msg.Params, &updateParams); err != nil {
					continue
				}
				// Ignore stale updates from previous sessions.
				if updateParams.SessionID != sessionID {
					continue
				}

				switch updateParams.Update.SessionUpdate {
				case "tool_call", "tool_call_update":
					toolUpdateCount++
				}

				if text, ok := agentMessageChunkText(updateParams.Update); ok {
					// Send text chunk immediately.
					textCh <- text
					receivedText = true
					agentTextAfterLatestTool = true
					continue
				}

				if text, ok := toolCallUpdateText(updateParams.Update); ok {
					rememberToolContent(updateParams.Update, text)
				}
			}
		}
	}
}

// Compile-time check
var _ Transport = (*ACPTransport)(nil)
