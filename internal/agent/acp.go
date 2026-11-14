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
	"syscall"
	"time"

	"github.com/tfcace/hash/internal/trace"
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
//
// Lifecycle (link): disconnected until a request needs the agent; connect
// spawns and initializes a process and the link is connected; Close ends it
// for good (closed), and only Restart reopens it. A connection carries at
// most one session and is torn down in exactly one place (dropConnLocked),
// with a reason: a lost connection (broken pipe, stdout ended, idle timeout,
// failed initialize) keeps a session the agent can resume as the resume
// candidate, Close forgets it.
type ACPTransport struct {
	config    ACPConfig
	requestID atomic.Int64

	// exchange admits one reader of the connection's message stream at a time:
	// a prompt turn or a plain request. Without it a model switch during a turn
	// would read the same stream and each side would drop the other's replies.
	exchange chan struct{}

	mu   sync.Mutex
	link link // the agent process: disconnected, connected or closed
	// resume is the session to resume on the next connection: set when a
	// connection with a resumable session is lost, cleared when a session
	// starts and by Close and Restart. nil means the next connection starts
	// a fresh session.
	resume *acpSession
	// model describes the model option of the most recent session. Every
	// session start replaces it; after a lost connection it is the last
	// known value. The zero value means unknown or none: no session since
	// Close or Restart, or a session with no model selection (CurrentModel
	// and AvailableModels report nothing either way).
	model modelConfig
	// preferredModel is the user's sticky choice (an option value),
	// re-applied to each new session; "" means no pin. Close keeps it; a
	// session that no longer offers it drops it.
	preferredModel string

	// Permission handler callback
	permissionHandler  func(ctx context.Context, req ToolPermissionRequest) (allow bool, always bool)
	permissionPromptMu sync.Mutex

	idle time.Duration // how long a turn waits for the agent; IdleTimeout unless a test shortens it
}

// linkState is where the transport stands with its agent process.
type linkState int

const (
	linkDisconnected linkState = iota // no process; the next request spawns one
	linkConnected                     // conn is the live process
	linkClosed                        // Close ended it; nothing reconnects until Restart
)

// link is the transport's hold on an agent process; conn is set only while
// connected.
type link struct {
	state linkState
	conn  *acpConn
}

// dropReason says why a connection ended, which decides what survives it.
type dropReason int

const (
	notDropped dropReason = iota // the connection is live
	dropLost                     // the process failed us; a resumable session survives
	dropClosed                   // hash closed it (Close, Restart); nothing survives
)

// acpConn is one agent process. Everything that is only meaningful for that
// process lives here, so a teardown cannot leave half of it behind and a
// goroutine holding an old conn can never write to a newer process.
type acpConn struct {
	cmd      *exec.Cmd // nil for in-memory test connections
	stdin    io.WriteCloser
	writeMu  sync.Mutex    // one JSON-RPC line at a time on stdin
	messages chan []byte   // agent output lines; closed by readLoop when stdout ends
	closed   chan struct{} // closed by shutdown so readLoop never blocks on a dead conn

	// Guarded by ACPTransport.mu.
	capabilities acpCapabilities
	session      *acpSession // nil: no session on this connection
	dropped      dropReason  // why the connection ended; notDropped while live
}

// acpSession identifies an agent session and the directory it was opened in.
type acpSession struct {
	id  string
	cwd string
}

// modelConfig is a session's "model" config option. The zero value means the
// session exposes no model selection.
type modelConfig struct {
	id      string        // option id
	current string        // selected value
	options []ModelOption // advertised choices
}

func modelConfigFrom(opts []configOption) modelConfig {
	for _, opt := range opts {
		if opt.Category != "model" {
			continue
		}
		m := modelConfig{id: opt.ID, current: opt.CurrentValue}
		for _, v := range opt.Options {
			m.options = append(m.options, ModelOption(v))
		}
		return m
	}
	return modelConfig{}
}

func (m modelConfig) offers(value string) bool {
	for _, o := range m.options {
		if o.Value == value {
			return true
		}
	}
	return false
}

func newACPConn(stdin io.WriteCloser) *acpConn {
	return &acpConn{
		stdin:    stdin,
		messages: make(chan []byte, 1024),
		closed:   make(chan struct{}),
	}
}

// write sends one JSON-RPC line.
func (c *acpConn) write(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.stdin.Write(append(data, '\n'))
	return err
}

// respond answers a request the agent sent on this connection.
func (c *acpConn) respond(id int64, result interface{}, rpcErr *jsonRPCError) {
	data, err := json.Marshal(jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
	if err != nil {
		return
	}
	_ = c.write(data) // best effort: a dead agent is noticed by the reader
}

// readLoop forwards agent output lines until stdout ends or the conn shuts
// down. ended, when not nil, runs first when it stops: the conn can never
// answer another request.
func (c *acpConn) readLoop(r io.Reader, ended func()) {
	defer close(c.messages)
	if ended != nil {
		defer ended()
	}
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		select {
		case c.messages <- line: // backpressure instead of dropping replies
		case <-c.closed:
			return
		}
	}
}

// shutdown ends the agent process. Called once, by dropConnLocked.
func (c *acpConn) shutdown() {
	close(c.closed)
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		// The whole group: the adapter's own children go with it.
		if syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL) != nil {
			_ = c.cmd.Process.Kill() // best effort
		}
		_ = c.cmd.Wait() // reaps the process and closes the stdout pipe
	}
}

// acpMessage is any JSON-RPC line from the agent: a response (id, no method),
// a request (id and method) or a notification (method, no id).
type acpMessage struct {
	ID     *int64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
}

func (m *acpMessage) isRequest() bool { return m.ID != nil && m.Method != "" }

func (m *acpMessage) isResponseTo(id int64) bool {
	return m.ID != nil && m.Method == "" && *m.ID == id
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

// rpcError is the agent's error reply as hash reads it, with the kind its
// data names, if any.
func (e *jsonRPCError) rpcError() *RPCError {
	var data struct {
		ErrorKind string `json:"errorKind"`
	}
	if len(e.Data) > 0 && json.Unmarshal(e.Data, &data) != nil {
		data.ErrorKind = "" // data of another shape: no kind
	}
	return &RPCError{Code: e.Code, Message: e.Message, ErrorKind: data.ErrorKind}
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
	SessionID     string         `json:"sessionId"`
	ConfigOptions []configOption `json:"configOptions"`
}

// configOption mirrors an entry of the ACP session "configOptions" array.
// We only act on the entry whose category is "model", but decode the shared shape.
type configOption struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	Category     string              `json:"category"`
	Type         string              `json:"type"`
	CurrentValue string              `json:"currentValue"`
	Options      []configOptionValue `json:"options"`
}

type configOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// setConfigOptionParams is the request body for session/set_config_option.
type setConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

// setConfigOptionResult is the response: the full updated config option set.
type setConfigOptionResult struct {
	ConfigOptions []configOption `json:"configOptions"`
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
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
	ConfigOptions []configOption  `json:"configOptions,omitempty"`
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
		Kind       string          `json:"kind"`
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

// permissionCanceled is the ACP outcome for a permission request the client
// did not put to the user.
const permissionCanceled = "cancelled" //nolint:misspell // ACP wire value

type permissionOutcome struct {
	Outcome  string `json:"outcome"`            // "selected" or permissionCanceled
	OptionID string `json:"optionId,omitempty"` // Which option was selected; none for permissionCanceled
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
		exchange: make(chan struct{}, 1),
		idle:     IdleTimeout,
	}
}

// SetPermissionHandler sets the callback for handling permission requests.
// The callback receives a ToolPermissionRequest and returns (allow, always).
// If always is true, the command should be added to the allowlist.
// The context is the prompt turn's context: when the turn is canceled the
// handler must stop waiting for user input and return (deny).
func (t *ACPTransport) SetPermissionHandler(handler func(ctx context.Context, req ToolPermissionRequest) (allow bool, always bool)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.permissionHandler = handler
}

// Name returns the transport name.
func (t *ACPTransport) Name() string {
	return "acp"
}

// beginExchange waits for exclusive use of the message stream. Every public
// entry point that reads agent replies holds it; Close does not, so it can
// always tear a stuck connection down.
func (t *ACPTransport) beginExchange(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case t.exchange <- struct{}{}:
		return func() { <-t.exchange }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Connect starts the agent process and initializes the ACP session.
func (t *ACPTransport) Connect(ctx context.Context) error {
	release, err := t.beginExchange(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = t.connect(ctx)
	return err
}

// currentConn returns the live connection, or nil while not connected.
func (t *ACPTransport) currentConn() *acpConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.link.conn
}

// liveConn returns the live connection, or why there is none.
func (t *ACPTransport) liveConn() (*acpConn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.liveConnLocked()
}

func (t *ACPTransport) liveConnLocked() (*acpConn, error) {
	switch t.link.state {
	case linkConnected:
		return t.link.conn, nil
	case linkClosed:
		return nil, ErrACPTransportClosed
	default:
		return nil, fmt.Errorf("%w: not connected", ErrACPConnectionClosed)
	}
}

// connect returns the live connection, spawning and initializing the agent
// when disconnected. A connected link is live: a connection whose output
// ends (the agent died between requests) is lost the moment its read loop
// ends (spawn), so a prompt is never written to a process nobody listens
// to. The caller holds the exchange, so nobody else reads the new
// connection's messages while initialize waits for its reply.
func (t *ACPTransport) connect(ctx context.Context) (*acpConn, error) {
	t.mu.Lock()
	if t.link.state != linkDisconnected {
		c, err := t.liveConnLocked()
		t.mu.Unlock()
		return c, err
	}
	c, err := t.spawn()
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	t.link = link{state: linkConnected, conn: c} // before initialize, so Close can kill a hung startup
	t.mu.Unlock()

	if err := t.initialize(ctx, c); err != nil {
		if lostErr := t.lost(c, err); errors.Is(lostErr, ErrACPTransportClosed) {
			return nil, lostErr
		}
		return nil, fmt.Errorf("initialize: %w", err)
	}
	return c, nil
}

// lost ends c after a failure on it (write error, stdout ended, idle
// timeout, failed initialize) and returns the error to report: the cause, as
// the next request reconnects, unless hash closed c meanwhile (Close, or a
// Restart that may already have a new process), which is final for c's
// request: retrying it would replay it.
func (t *ACPTransport) lost(c *acpConn, cause error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.link.state == linkClosed || c.dropped == dropClosed {
		return ErrACPTransportClosed
	}
	t.dropConnLocked(c, dropLost)
	return cause
}

// spawn starts the agent process and its read loop.
func (t *ACPTransport) spawn() (*acpConn, error) {
	program, args := t.config.ParsedCommand()
	trace.Agent("acp_start", map[string]any{"program": program})
	cmd := exec.Command(program, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.Join(ErrACPStartFailed, fmt.Errorf("stdin pipe: %w", err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, errors.Join(ErrACPStartFailed, fmt.Errorf("stdout pipe: %w", err))
	}
	cmd.Stderr = nil // discard stderr to avoid blocking
	// Its own process group: the terminal's Ctrl+C goes to hash's foreground
	// group and must not kill the adapter mid-conversation.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, errors.Join(ErrACPStartFailed, fmt.Errorf("start agent: %w", err))
	}
	trace.Agent("acp_started", map[string]any{"pid": cmd.Process.Pid})
	c := newACPConn(stdin)
	c.cmd = cmd
	// The end of its output loses the connection (a resumable session
	// survives); after a shutdown it changes nothing.
	go c.readLoop(stdout, func() { _ = t.lost(c, ErrACPConnectionClosed) })
	return c, nil
}

// dropConnLocked is the one teardown path: c, if it is still the current
// connection, ends and the link is disconnected. A stale c (already replaced
// or closed) is left alone, so a late error on an old connection cannot kill
// its successor. Must be called with mu held.
func (t *ACPTransport) dropConnLocked(c *acpConn, why dropReason) {
	if c == nil || t.link.conn != c {
		return
	}
	t.link = link{state: linkDisconnected}
	c.dropped = why
	if why == dropLost && c.capabilities.SessionResume && c.session != nil {
		t.resume = c.session
	}
	c.shutdown()
}

// resetConnection drops the current connection so the next request
// reconnects (and resumes the session when the agent supports it).
func (t *ACPTransport) resetConnection() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dropConnLocked(t.link.conn, dropLost)
}

// Close terminates the agent process and forgets its session. It is final:
// a request in flight ends with ErrACPTransportClosed and nothing spawns a
// new process until Restart.
func (t *ACPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endLocked(linkClosed)
	return nil
}

// Restart ends the agent process and session, as Close does, and connects a
// new one (a freshly installed adapter answers). The user's model pin is
// re-applied to the new session. The old process ends and the link reopens
// in one critical section; a request still on the old process ends as it
// would after Close (see lost).
func (t *ACPTransport) Restart(ctx context.Context) error {
	t.mu.Lock()
	t.endLocked(linkDisconnected)
	t.mu.Unlock()
	return t.EnsureModelInfo(ctx)
}

// endLocked ends the agent process and forgets its session, leaving the
// link in state: closed for Close, disconnected for Restart. Must be called
// with mu held.
func (t *ACPTransport) endLocked(state linkState) {
	t.dropConnLocked(t.link.conn, dropClosed)
	t.link = link{state: state}
	t.resume = nil
	t.model = modelConfig{}
	// preferredModel stays: it is the user's pin, and ensureSession re-applies
	// it to the next session (or drops it if that session no longer offers it).
}

// acpCancelTimeout bounds how long a canceled turn waits for the agent to
// stop; an agent that does not is treated as lost.
const acpCancelTimeout = 1500 * time.Millisecond

// notifyCancel sends the session/cancel notification: the agent stops the
// session's prompt turn and answers it with the canceled stop reason.
func (c *acpConn) notifyCancel(sessionID string) {
	// Notifications don't have an ID field.
	data, err := json.Marshal(struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params"`
	}{
		JSONRPC: "2.0",
		Method:  "session/cancel",
		Params:  closeSessionParams{SessionID: sessionID},
	})
	if err == nil {
		_ = c.write(data) // best effort
	}
}

// sendRequest sends one request on the current connection and waits for its
// reply. The caller holds the exchange. A canceled wait leaves the session
// alone: the late reply is skipped by whoever reads the stream next.
func (t *ACPTransport) sendRequest(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	c, err := t.liveConn()
	if err != nil {
		return nil, err
	}
	id := t.requestID.Add(1)
	trace.Agent("acp_request", map[string]any{"id": id, "method": method})
	data, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if err := c.write(data); err != nil {
		// The pipe is broken (e.g. the agent exited); the next request
		// reconnects.
		return nil, t.lost(c, errors.Join(ErrACPConnectionClosed, fmt.Errorf("write request: %w", err)))
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case line, ok := <-c.messages:
			if !ok {
				return nil, t.lost(c, fmt.Errorf("%w: connection closed", ErrACPConnectionClosed))
			}
			var msg acpMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			if msg.isRequest() {
				// No prompt turn is running, so there is nothing to grant;
				// answer anyway or the agent waits forever.
				t.handleIncomingRequest(ctx, c, "", *msg.ID, msg.Method, msg.Params)
				continue
			}
			if msg.isResponseTo(id) {
				trace.Agent("acp_response", map[string]any{"id": id, "method": method})
				if msg.Error != nil {
					return nil, msg.Error.rpcError()
				}
				return msg.Result, nil
			}
			// A notification or a stale reply: not ours, keep waiting.
		}
	}
}

func (t *ACPTransport) initialize(ctx context.Context, c *acpConn) error {
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
	c.capabilities = initResult.AgentCapabilities.normalized()
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

func (t *ACPTransport) newSession(ctx context.Context, cwd string) (string, []configOption, error) {
	params := newSessionParams{
		Cwd:        normalizeSessionCWD(cwd),
		McpServers: []interface{}{},
	}

	result, err := t.sendRequest(ctx, "session/new", params)
	if err != nil {
		return "", nil, err
	}

	var sessionResult newSessionResult
	if err := json.Unmarshal(result, &sessionResult); err != nil {
		return "", nil, fmt.Errorf("parse session result: %w", err)
	}
	return sessionResult.SessionID, sessionResult.ConfigOptions, nil
}

// resumeSession returns the resumed session's config options, which can
// differ from what the previous process had (an agent restarted by a reset
// comes back on its default model).
func (t *ACPTransport) resumeSession(ctx context.Context, sessionID, cwd string) ([]configOption, error) {
	params := resumeSessionParams{
		SessionID:  sessionID,
		Cwd:        normalizeSessionCWD(cwd),
		McpServers: []interface{}{},
	}

	result, err := t.sendRequest(ctx, "session/resume", params)
	if err != nil {
		return nil, err
	}
	var res setConfigOptionResult // same {"configOptions": [...]} shape
	_ = json.Unmarshal(result, &res)
	return res.ConfigOptions, nil
}

// ensureSession returns the current connection's session, resuming the
// resume candidate or opening a new session when there is none.
//
// The session keeps the directory it was opened in when the shell changes
// directory: a new session per cd would drop the conversation, and every
// prompt already states the shell's current working directory.
func (t *ACPTransport) ensureSession(ctx context.Context, cwd string) (string, error) {
	normalizedCWD := normalizeSessionCWD(cwd)

	t.mu.Lock()
	c, err := t.liveConnLocked()
	if err != nil {
		t.mu.Unlock()
		return "", err
	}
	if c.session != nil {
		sessionID := c.session.id
		t.mu.Unlock()
		return sessionID, nil
	}
	resume := t.resume
	canResume := c.capabilities.SessionResume
	t.mu.Unlock()

	if canResume && resume != nil {
		session := *resume
		if session.cwd == "" {
			session.cwd = normalizedCWD
		}
		opts, resumeErr := t.resumeSession(ctx, session.id, session.cwd)
		if resumeErr == nil {
			t.startSession(c, &session, opts, true)
			t.reapplyPreferredModel(ctx)
			return session.id, nil
		} else if IsRetryableError(resumeErr) {
			return "", resumeErr
		}

		t.mu.Lock()
		if t.resume == resume {
			t.resume = nil
		}
		t.mu.Unlock()
	}

	newSessionID, opts, err := t.newSession(ctx, normalizedCWD)
	if err != nil {
		return "", err
	}
	t.startSession(c, &acpSession{id: newSessionID, cwd: normalizedCWD}, opts, false)
	t.reapplyPreferredModel(ctx)
	return newSessionID, nil
}

// startSession makes s the connection's session and replaces the model cache
// with what the session reports. A resumed session that reports nothing keeps
// the cache: it is the same session the cache describes.
func (t *ACPTransport) startSession(c *acpConn, s *acpSession, opts []configOption, resumed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c.session = s
	t.resume = nil
	if len(opts) > 0 || !resumed {
		t.model = modelConfigFrom(opts)
	}
}

// storeModelConfig refreshes the model cache from a config snapshot sent for
// the current session (config_option_update, set_config_option reply).
func (t *ACPTransport) storeModelConfig(opts []configOption) {
	// Guard against a malformed/empty notification wiping known state. ACP sends
	// the complete configOptions array, so a real "no model option" agent still
	// carries its other options here and is handled correctly below.
	if len(opts) == 0 {
		return
	}
	m := modelConfigFrom(opts)
	t.mu.Lock()
	t.model = m
	t.mu.Unlock()
}

// CurrentModel returns the display name of the active model, or "" when the
// agent exposes no model option.
func (t *ACPTransport) CurrentModel() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.model.id == "" {
		return ""
	}
	for _, m := range t.model.options {
		if m.Value == t.model.current {
			return m.Name
		}
	}
	return t.model.current
}

// AvailableModels returns a copy of the advertised model choices, or nil.
func (t *ACPTransport) AvailableModels() []ModelOption {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.model.options) == 0 {
		return nil
	}
	return append([]ModelOption(nil), t.model.options...)
}

// EnsureModelInfo connects and establishes a session so the cached model
// information reflects what the agent advertises.
func (t *ACPTransport) EnsureModelInfo(ctx context.Context) error {
	release, err := t.beginExchange(ctx)
	if err != nil {
		return err
	}
	defer release()
	return t.ensureModelInfo(ctx)
}

func (t *ACPTransport) ensureModelInfo(ctx context.Context) error {
	if _, err := t.connect(ctx); err != nil {
		return err
	}
	_, err := t.ensureSession(ctx, "")
	return err
}

// SetModel selects a model by value and remembers it as the session preference
// so it survives transparent reconnects.
func (t *ACPTransport) SetModel(ctx context.Context, value string) error {
	release, err := t.beginExchange(ctx)
	if err != nil {
		return err
	}
	defer release()

	if err := t.ensureModelInfo(ctx); err != nil {
		return err
	}
	if err := t.setModel(ctx, value); err != nil {
		return err
	}

	t.mu.Lock()
	t.preferredModel = value
	t.mu.Unlock()
	return nil
}

// setModel sends session/set_config_option for the model option and updates the
// cached state from the agent's response.
func (t *ACPTransport) setModel(ctx context.Context, value string) error {
	t.mu.Lock()
	var sessionID string
	if c := t.link.conn; c != nil && c.session != nil {
		sessionID = c.session.id
	}
	configID := t.model.id
	t.mu.Unlock()
	if configID == "" {
		return fmt.Errorf("agent does not expose model selection")
	}
	if sessionID == "" {
		return fmt.Errorf("no active agent session")
	}

	result, err := t.sendRequest(ctx, "session/set_config_option", setConfigOptionParams{
		SessionID: sessionID,
		ConfigID:  configID,
		Value:     value,
	})
	if err != nil {
		return err
	}

	var res setConfigOptionResult
	if jsonErr := json.Unmarshal(result, &res); jsonErr == nil && len(res.ConfigOptions) > 0 {
		t.storeModelConfig(res.ConfigOptions)
	} else {
		// Agent returned no config snapshot; trust the value we just set.
		t.mu.Lock()
		t.model.current = value
		t.mu.Unlock()
	}
	return nil
}

// reapplyPreferredModel re-selects the user's sticky model choice after a
// session start, comparing against the model that session reported. No-op
// when nothing is pinned or the choice already matches; a pin the session no
// longer offers is dropped.
func (t *ACPTransport) reapplyPreferredModel(ctx context.Context) {
	t.mu.Lock()
	preferred := t.preferredModel
	model := t.model
	t.mu.Unlock()

	if preferred == "" || model.id == "" || preferred == model.current {
		return
	}
	if !model.offers(preferred) {
		t.mu.Lock()
		t.preferredModel = ""
		t.mu.Unlock()
		return
	}
	// Best effort: a failure here just leaves the session on the default model.
	_ = t.setModel(ctx, preferred)
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

	// A bare command, or a one-line lead-in followed by one command line
	if cmd, ok := commandFromResponse(text); ok {
		return Response{
			Type:    ResponseTypeCommand,
			Command: cmd,
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

// handleIncomingRequest answers a request the agent sent on c (like
// session/request_permission). turnSession is the session of the prompt turn
// that received it, or "" outside a turn. ctx is the turn's context;
// cancellation propagates to handlers that wait on user input.
func (t *ACPTransport) handleIncomingRequest(ctx context.Context, c *acpConn, turnSession string, id int64, method string, params json.RawMessage) {
	switch method {
	case "session/request_permission":
		t.handleRequestPermission(ctx, c, turnSession, id, params)
	default:
		c.respond(id, nil, &jsonRPCError{
			Code:    -32601,
			Message: "Method not found",
		})
	}
}

// handleRequestPermission handles permission requests from the agent. Only a
// request for the running turn's session reaches the user; anything else is
// left over from a canceled turn and is answered with the canceled outcome.
func (t *ACPTransport) handleRequestPermission(ctx context.Context, c *acpConn, turnSession string, id int64, params json.RawMessage) {
	var p requestPermissionParams
	if err := json.Unmarshal(params, &p); err != nil {
		c.respond(id, nil, &jsonRPCError{
			Code:    -32602,
			Message: "Invalid params",
		})
		return
	}
	if turnSession == "" || p.SessionID != turnSession {
		c.respond(id, permissionResponse{Outcome: &permissionOutcome{Outcome: permissionCanceled}}, nil)
		return
	}

	reject := permissionOutcome{Outcome: "selected", OptionID: resolveOptionID(p.Options, "reject_once", "reject")}
	req, ok := permissionRequestFrom(p)
	t.mu.Lock()
	handler := t.permissionHandler
	t.mu.Unlock()
	if !ok || handler == nil {
		// Unknown command or no handler: deny by default.
		c.respond(id, permissionResponse{Outcome: &reject}, nil)
		return
	}

	if ctx == nil {
		ctx = context.Background()
	}
	t.permissionPromptMu.Lock()
	allow, always := handler(ctx, req)
	t.permissionPromptMu.Unlock()
	if ctx.Err() != nil {
		// The turn ended under the prompt: ACP wants the canceled outcome.
		c.respond(id, permissionResponse{Outcome: &permissionOutcome{Outcome: permissionCanceled}}, nil)
		return
	}

	outcome := reject
	switch {
	case allow && always:
		outcome = permissionOutcome{Outcome: "selected", OptionID: resolveOptionID(p.Options, "allow_always", "allow_always")}
	case allow:
		outcome = permissionOutcome{Outcome: "selected", OptionID: resolveOptionID(p.Options, "allow_once", "allow")}
	}
	c.respond(id, permissionResponse{Outcome: &outcome}, nil)
}

// permissionRequestFrom describes the tool call for the user. ok is false when
// the command the agent wants to run cannot be determined.
func permissionRequestFrom(p requestPermissionParams) (req ToolPermissionRequest, ok bool) {
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

	// Extract tool name from rawInput if available, then fall back to the
	// standard ACP kind. Some ACP agents deliberately omit rawInput.
	toolName := extractToolName(p.ToolCall.RawInput)
	if toolName == "" {
		toolName = p.ToolCall.Kind
	}
	return ToolPermissionRequest{
		Command:    command,
		ToolName:   toolName,
		ToolCallID: p.ToolCall.ToolCallID,
	}, command != ""
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

// buildPromptWithContext builds a prompt that includes context information.
// acpReplyContract tells the agent how Hash presents its reply, so the choice
// between answering with a command and answering with output is an informed
// one. Inline completions carry their own instructions and skip it.
const acpReplyContract = "You are answering inside the Hash shell. " +
	"If your reply is a single bare shell command on one line, the shell offers to run it; " +
	"anything else is shown as output. " +
	"If a tool call is denied, reply with the command you would have run instead.\n\n"

func buildPromptWithContext(req Request) string {
	var b strings.Builder

	if !req.Inline {
		b.WriteString(acpReplyContract)
	}
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

// IdleTimeout is the maximum time to wait without receiving any message from the agent.
// If exceeded, the request is considered stuck and canceled.
const IdleTimeout = 30 * time.Second

// idleTimeoutForContext is how long a turn waits for the next message: idle,
// or the context's whole remaining time when that is longer.
func idleTimeoutForContext(ctx context.Context, idle time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > idle {
			return remaining
		}
	}
	return idle
}

// SendStreaming implements the legacy text-only Transport interface. Rich ACP
// callers should use SendEventStream through Client.StreamEvents instead.
//
//nolint:gocritic // interface parity with Transport
func (t *ACPTransport) SendStreaming(ctx context.Context, req Request) (<-chan string, <-chan error) {
	textCh := make(chan string, 64)
	errCh := make(chan error, 1)
	events, eventErrs := t.SendEventStream(ctx, req)

	go func() {
		defer close(textCh)
		defer close(errCh)
		// Text first, then the error: SendEventStream reports its error
		// before closing events, so forwarding in this order keeps a reader
		// that stops at the error from missing text. A reader that stops
		// reading is released by ctx; the event stream stops on ctx too.
		for event := range events {
			if event.Type != StreamEventText || event.Text == "" {
				continue
			}
			select {
			case textCh <- event.Text:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
		for err := range eventErrs {
			if err != nil {
				errCh <- err
			}
		}
	}()

	return textCh, errCh
}

// SendEventStream exposes the standard ACP text and tool lifecycle events.
//
//nolint:gocritic // interface parity with Transport
func (t *ACPTransport) SendEventStream(ctx context.Context, req Request) (<-chan StreamEvent, <-chan error) {
	eventCh := make(chan StreamEvent, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(eventCh)
		defer close(errCh)

		release, err := t.beginExchange(ctx)
		if err != nil {
			errCh <- err
			return
		}
		defer release()
		if err := t.streamEventsWithRetry(ctx, req, eventCh, t.sendEventStreamingAttempt, t.resetConnection); err != nil {
			errCh <- err
		}
	}()

	return eventCh, errCh
}

// streamEventsWithRetry retries only a prompt that never reached the agent
// (the connection failed before or while sending it). Once the agent has
// the prompt, a resend would put it in the conversation twice (a resumed
// session keeps it) and could repeat what the turn showed or did, so a
// later failure (no output, idle timeout, a lost process) is the user's to
// retry.
func (t *ACPTransport) streamEventsWithRetry(
	ctx context.Context,
	req Request,
	eventCh chan<- StreamEvent,
	attemptFn func(context.Context, Request, chan<- StreamEvent) (bool, error),
	resetFn func(),
) error {
	const maxAttempts = 2 // Initial try + one transparent reconnect retry.
	for attempt := 0; attempt < maxAttempts; attempt++ {
		delivered, err := attemptFn(ctx, req, eventCh)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt < maxAttempts-1 && !delivered && IsRetryableError(err) {
			resetFn()
			continue
		}
		return err
	}
	return nil
}

// sendEventStreamingAttempt performs one ACP streaming attempt. delivered
// reports that the agent received the prompt (everything the user can see
// comes after that).
func (t *ACPTransport) sendEventStreamingAttempt(
	ctx context.Context,
	req Request,
	eventCh chan<- StreamEvent,
) (delivered bool, retErr error) {
	c, err := t.connect(ctx)
	if err != nil {
		return false, err
	}
	sessionID, err := t.ensureSession(ctx, req.Context.Cwd)
	if err != nil {
		return false, err
	}

	id := t.requestID.Add(1)
	data, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "session/prompt",
		Params: promptParams{
			SessionID: sessionID,
			Prompt:    []promptPart{{Type: "text", Text: buildPromptWithContext(req)}},
		},
	})
	if err != nil {
		return false, err
	}
	if err = c.write(data); err != nil {
		// Write failed - a retry can lazily reconnect.
		return false, t.lost(c, errors.Join(ErrACPConnectionClosed, fmt.Errorf("write prompt: %w", err)))
	}
	trace.Agent("acp_prompt_sent", map[string]any{"id": id})

	// The turn's own context ends with the turn, releasing any permission
	// prompt the agent left open (it answered the prompt without waiting).
	turnCtx, endTurn := context.WithCancel(ctx)
	defer endTurn()
	turn := &promptTurn{
		t:         t,
		c:         c,
		ctx:       turnCtx,
		events:    eventCh,
		sessionID: sessionID,
		requestID: id,
	}
	return true, turn.run()
}

// promptTurn is one session/prompt exchange: it reads the connection until
// the prompt's reply arrives and tracks what the user has seen so far.
type promptTurn struct {
	t         *ACPTransport
	c         *acpConn
	ctx       context.Context // ends with the turn; permission prompts run on it
	events    chan<- StreamEvent
	sessionID string
	requestID int64

	// What the user has seen of the reply, changed only by showText and
	// showTool.
	last      lastShown
	textShown bool // reply text was shown
	// unanswered is the tool text since the last agent text, by tool call
	// id in arrival order; it stands in for the reply if the turn ends on it.
	unanswered  []toolText
	toolUpdates int

	permissionsInFlight atomic.Int32 // local prompts the agent is blocked on
}

// lastShown is what the user saw last in a turn's reply.
type lastShown int

const (
	shownNothing  lastShown = iota
	shownText               // agent text
	shownTool               // tool activity with no text of its own since the last agent text
	shownToolText           // a tool's text since the last agent text
)

type toolText struct{ id, text string }

// turnEnd is how a prompt turn stopped reading the connection.
type turnEnd int

const (
	turnReplied  turnEnd = iota // the prompt's reply arrived
	turnCanceled                // the turn's context ended first
	turnLost                    // the connection failed (stdout ended, idle timeout)
)

// run reads the turn to its end. Every way out goes through end, so a turn
// whose context ended before the reply always cancels and drains, wherever
// the read loop noticed it (waiting for a message or sending an event).
func (p *promptTurn) run() error {
	return p.end(p.read())
}

// end finishes the turn for how it ended. A canceled turn sends
// session/cancel and reads on until the agent answers, so nothing of it
// reaches the next turn; a lost one drops the connection.
func (p *promptTurn) end(how turnEnd, err error) error {
	switch how {
	case turnCanceled:
		p.cancel()
		return p.ctx.Err()
	case turnLost:
		return p.t.lost(p.c, err)
	default:
		return err
	}
}

// read handles agent messages until the turn ends, and says how it did.
func (p *promptTurn) read() (turnEnd, error) {
	idleTimeout := idleTimeoutForContext(p.ctx, p.t.idle)
	idleTimer := time.NewTimer(idleTimeout)
	defer idleTimer.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return turnCanceled, nil

		case <-idleTimer.C:
			// Don't time out while we're waiting on a local permission prompt.
			// The agent is blocked on our response, so this is user think-time,
			// not transport idleness.
			if p.permissionsInFlight.Load() > 0 {
				idleTimer.Reset(idleTimeout)
				continue
			}
			// No message received for IdleTimeout - agent may be stuck.
			// Drop the process (it may be hung) but not the conversation:
			// the next connection resumes the session when it can.
			p.c.notifyCancel(p.sessionID)
			return turnLost, errors.Join(
				ErrACPIdleTimeout,
				fmt.Errorf("agent idle timeout (%v without response)", idleTimeout),
			)

		case line, ok := <-p.c.messages:
			if !ok {
				// The agent's stdout ended - reconnect on the next request.
				return turnLost, fmt.Errorf("%w: connection closed", ErrACPConnectionClosed)
			}
			idleTimer.Reset(idleTimeout)
			var msg acpMessage
			if json.Unmarshal(line, &msg) != nil {
				continue // not JSON-RPC; ignore
			}
			if done, err := p.handle(&msg); done {
				return turnReplied, err
			} else if err != nil {
				return turnCanceled, nil // an event send stopped on the ended context
			}
		}
	}
}

// cancel stops the agent's work on a turn the user canceled and keeps the
// session, so the conversation survives: it sends session/cancel and reads
// on until the agent answers the prompt, so nothing of the canceled turn
// reaches the next one. Requests meanwhile get the canceled outcome. An
// agent that does not stop in time is treated as lost (a resumable session
// survives that too).
func (p *promptTurn) cancel() {
	p.c.notifyCancel(p.sessionID)
	timer := time.NewTimer(acpCancelTimeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			_ = p.t.lost(p.c, ErrACPIdleTimeout)
			return
		case line, ok := <-p.c.messages:
			if !ok {
				_ = p.t.lost(p.c, ErrACPConnectionClosed)
				return
			}
			var msg acpMessage
			if json.Unmarshal(line, &msg) != nil {
				continue
			}
			switch {
			case msg.isResponseTo(p.requestID):
				return
			case msg.isRequest():
				p.t.handleIncomingRequest(p.ctx, p.c, "", *msg.ID, msg.Method, msg.Params)
			}
		}
	}
}

// handle processes one agent message. done reports the prompt's reply;
// before that, an error means the turn's context ended mid-send.
func (p *promptTurn) handle(msg *acpMessage) (done bool, err error) {
	switch {
	case msg.isRequest():
		p.permissionsInFlight.Add(1)
		go func(id int64, method string, params json.RawMessage) {
			defer p.permissionsInFlight.Add(-1)
			p.t.handleIncomingRequest(p.ctx, p.c, p.sessionID, id, method, params)
		}(*msg.ID, msg.Method, msg.Params)
		return false, nil
	case msg.isResponseTo(p.requestID):
		return true, p.finish(msg)
	case msg.Method == "session/update":
		var params sessionUpdateParams
		// Ignore malformed updates and stale ones from previous sessions.
		if json.Unmarshal(msg.Params, &params) == nil && params.SessionID == p.sessionID {
			return false, p.update(params.Update)
		}
	}
	return false, nil
}

// finish handles the prompt's reply.
func (p *promptTurn) finish(msg *acpMessage) error {
	if msg.Error != nil {
		return msg.Error.rpcError()
	}
	if err := p.emitToolFallback(); err != nil {
		return err
	}
	if !p.textShown {
		return noOutputPromptError(msg.Result, p.toolUpdates)
	}
	return nil
}

// update handles one session update. Its only error is an event send that
// stopped because the turn's context ended.
func (p *promptTurn) update(update sessionUpdate) error {
	switch update.SessionUpdate {
	case "tool_call", "tool_call_update":
		content, _ := toolCallUpdateText(update)
		p.showTool(update.ToolCallID, content)
		if !p.emit(StreamEvent{
			Type: StreamEventToolCall,
			ToolCall: ToolCallUpdate{
				ID:      update.ToolCallID,
				Title:   update.Title,
				Kind:    update.Kind,
				Status:  ToolCallStatus(update.Status),
				Content: content,
			},
		}) {
			return p.ctx.Err()
		}
		return nil
	case "config_option_update":
		// The agent switched a config option on its own (e.g. model
		// fallback on rate limit); refresh our cached model state.
		p.t.storeModelConfig(update.ConfigOptions)
		return nil
	}

	text, ok := agentMessageChunkText(update)
	if !ok {
		return nil
	}
	// Separate this text block from a preceding one that was split off by a
	// tool call. Adjacent blocks arrive with no separator and would glue
	// together ("Let me investigate." + "jj does see" -> "investigate.jj").
	if p.textShown && (p.last == shownTool || p.last == shownToolText) {
		if !p.emit(StreamEvent{Type: StreamEventText, Text: "\n\n"}) {
			return p.ctx.Err()
		}
	}
	if !p.emit(StreamEvent{Type: StreamEventText, Text: text}) {
		return p.ctx.Err()
	}
	p.showText()
	return nil
}

// showText records agent text: it answers the tool text before it.
func (p *promptTurn) showText() {
	p.last = shownText
	p.textShown = true
	p.unanswered = nil
}

// showTool records tool activity and the text it produced, if any.
func (p *promptTurn) showTool(id, text string) {
	p.toolUpdates++
	if text == "" {
		if p.last != shownToolText {
			p.last = shownTool
		}
		return
	}
	p.last = shownToolText
	if id == "" {
		id = fmt.Sprintf("__unkeyed_tool_%d", len(p.unanswered)+1)
	}
	for i := range p.unanswered {
		if p.unanswered[i].id == id {
			p.unanswered[i].text = text // a later update of the same call
			return
		}
	}
	p.unanswered = append(p.unanswered, toolText{id: id, text: text})
}

func (p *promptTurn) emit(event StreamEvent) bool {
	select {
	case p.events <- event:
		return true
	case <-p.ctx.Done():
		return false
	}
}

// emitToolFallback shows tool output as the reply when the agent ended the
// turn without commenting on its latest tool's output.
func (p *promptTurn) emitToolFallback() error {
	if p.last != shownToolText {
		return nil
	}
	var b strings.Builder
	for _, tt := range p.unanswered {
		appendTextPart(&b, tt.text)
	}
	text := b.String()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if p.textShown && !p.emit(StreamEvent{Type: StreamEventText, Text: "\n"}) {
		return p.ctx.Err()
	}
	if !p.emit(StreamEvent{Type: StreamEventText, Text: text}) {
		return p.ctx.Err()
	}
	p.showText()
	return nil
}

// Compile-time check
var _ Transport = (*ACPTransport)(nil)
var _ EventStreamTransport = (*ACPTransport)(nil)
