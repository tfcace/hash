package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sendStreamingAttempt preserves the legacy text-only assertions in this test
// file while production code exposes only the event stream implementation.
func (t *ACPTransport) sendStreamingAttempt(ctx context.Context, req Request, textCh chan<- string) (bool, error) {
	events := make(chan StreamEvent, 64)
	result := make(chan error, 1)
	go func() {
		_, err := t.sendEventStreamingAttempt(ctx, req, events)
		close(events)
		result <- err
	}()

	receivedText := false
	for event := range events {
		if event.Type == StreamEventText && event.Text != "" {
			textCh <- event.Text
			receivedText = true
		}
	}
	return receivedText, <-result
}

func TestACPTransport_New(t *testing.T) {
	cfg := ACPConfig{
		Command: "claude-agent-acp",
		Args:    []string{},
	}

	transport := NewACPTransport(cfg)
	if transport == nil {
		t.Fatal("NewACPTransport() returned nil")
	}
	if transport.Name() != "acp" {
		t.Errorf("Name() = %q, want %q", transport.Name(), "acp")
	}
}

func TestACPTransport_ImplementsInterface(t *testing.T) {
	var _ Transport = (*ACPTransport)(nil)
}

func TestACPConfig_ParsedCommand(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		args        []string
		wantProgram string
		wantArgs    []string
	}{
		{
			name:        "simple command",
			command:     "gemini",
			args:        nil,
			wantProgram: "gemini",
			wantArgs:    nil,
		},
		{
			name:        "command with embedded args",
			command:     "gemini --experimental-acp",
			args:        nil,
			wantProgram: "gemini",
			wantArgs:    []string{"--experimental-acp"},
		},
		{
			name:        "command with embedded args and explicit args",
			command:     "gemini --experimental-acp",
			args:        []string{"--model", "gemini-pro"},
			wantProgram: "gemini",
			wantArgs:    []string{"--experimental-acp", "--model", "gemini-pro"},
		},
		{
			name:        "command with multiple embedded args",
			command:     "my-agent --flag1 --flag2 value",
			args:        nil,
			wantProgram: "my-agent",
			wantArgs:    []string{"--flag1", "--flag2", "value"},
		},
		{
			name:        "command with only explicit args",
			command:     "claude",
			args:        []string{"--chat"},
			wantProgram: "claude",
			wantArgs:    []string{"--chat"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ACPConfig{
				Command: tt.command,
				Args:    tt.args,
			}

			program, args := cfg.ParsedCommand()

			if program != tt.wantProgram {
				t.Errorf("ParsedCommand() program = %q, want %q", program, tt.wantProgram)
			}

			if len(args) != len(tt.wantArgs) {
				t.Errorf("ParsedCommand() args = %v, want %v", args, tt.wantArgs)
				return
			}

			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("ParsedCommand() args[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

// Each connection owns its channels: a read loop that ends closes only its
// own conn's messages, never a successor's.
func TestACPTransport_ReadLoopClosesOnlyItsOwnChannel(t *testing.T) {
	transport, successor, _ := newPipeTransport("")

	pr, pw := io.Pipe()
	old := newACPConn(newMockPipe())
	go old.readLoop(pr, nil)

	if _, err := pw.Write([]byte("{\"jsonrpc\":\"2.0\"}\n")); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}

	select {
	case msg := <-old.messages:
		if string(msg) != "{\"jsonrpc\":\"2.0\"}\n" {
			t.Fatalf("unexpected message %q", string(msg))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}
	select {
	case _, ok := <-old.messages:
		if ok {
			t.Fatal("expected old messages channel to be closed after draining")
		}
	case <-time.After(time.Second):
		t.Fatal("old read loop did not close its channel")
	}

	// A late teardown of the old conn must not touch the successor.
	transport.mu.Lock()
	transport.dropConnLocked(old, dropLost)
	transport.mu.Unlock()
	if transport.currentConn() != successor {
		t.Fatal("dropping a stale conn replaced the current one")
	}
	select {
	case <-successor.messages:
		t.Fatal("successor messages channel should stay open and unused")
	default:
	}
}

// A read loop blocked on a full channel exits when its conn shuts down.
func TestACPConn_ReadLoopExitsOnShutdown(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	c := newACPConn(newMockPipe())
	c.messages = make(chan []byte) // unbuffered and never read
	go c.readLoop(pr, nil)
	go func() { _, _ = pw.Write([]byte("line\n")) }()
	time.Sleep(20 * time.Millisecond)
	c.shutdown()
	select {
	case _, ok := <-c.messages:
		if ok {
			t.Fatal("read loop delivered a line after shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("read loop did not exit after shutdown")
	}
}

func TestACPTransport_SendStreamingAttemptEmitsCompletedToolCallContent(t *testing.T) {
	transport, conn, _ := newPipeTransport("test-session")
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"docker-desktop\nkind-kind"}}]}}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)

	textCh := make(chan string, 10)
	receivedText, err := transport.sendStreamingAttempt(context.Background(), Request{Prompt: "list contexts"}, textCh)
	if err != nil {
		t.Fatalf("sendStreamingAttempt returned error: %v", err)
	}
	if !receivedText {
		t.Fatal("sendStreamingAttempt receivedText = false, want true")
	}

	var got strings.Builder
	for {
		select {
		case text := <-textCh:
			got.WriteString(text)
		default:
			if got.String() != "docker-desktop\nkind-kind" {
				t.Fatalf("streamed text = %q, want tool output", got.String())
			}
			return
		}
	}
}

func TestACPTransport_SendEventStreamingAttemptEmitsGenericToolLifecycle(t *testing.T) {
	transport, conn, _ := newPipeTransport("test-session")
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"pwd","kind":"execute","status":"pending"}}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed"}}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Done."}}}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)

	events := make(chan StreamEvent, 10)
	delivered, err := transport.sendEventStreamingAttempt(context.Background(), Request{Prompt: "test"}, events)
	if err != nil {
		t.Fatalf("sendEventStreamingAttempt returned error: %v", err)
	}
	if !delivered {
		t.Fatal("a turn the agent answered should count as delivered")
	}

	got := make([]StreamEvent, 0, 3)
	for len(events) > 0 {
		got = append(got, <-events)
	}
	if len(got) != 3 {
		t.Fatalf("event count = %d, want 3: %#v", len(got), got)
	}
	if got[0].Type != StreamEventToolCall || got[0].ToolCall.Title != "pwd" || got[0].ToolCall.Kind != "execute" || got[0].ToolCall.Status != ToolCallPending {
		t.Fatalf("initial tool event = %#v", got[0])
	}
	if got[1].ToolCall.ID != "call-1" || got[1].ToolCall.Status != ToolCallCompleted {
		t.Fatalf("completed tool event = %#v", got[1])
	}
	if got[2].Type != StreamEventText || got[2].Text != "Done." {
		t.Fatalf("text event = %#v", got[2])
	}
}

// An attempt the agent answered reports its prompt delivered, which rules
// out a retry (only a prompt that never reached the agent is sent again).
func TestACPTransport_SendEventStreamingAttemptReportsAnAnsweredPromptDelivered(t *testing.T) {
	transport, conn, _ := newPipeTransport("test-session")
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"partial"}}}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)

	events := make(chan StreamEvent, 2)
	delivered, err := transport.sendEventStreamingAttempt(context.Background(), Request{Prompt: "test"}, events)
	if err != nil {
		t.Fatalf("sendEventStreamingAttempt returned error: %v", err)
	}
	if !delivered {
		t.Fatal("a turn the agent answered should count as delivered and suppress a retry")
	}
}

// A delivered prompt is never sent again, whatever the error after it.
func TestACPTransport_StreamEventsWithRetryDoesNotResendADeliveredPrompt(t *testing.T) {
	transport := NewACPTransport(ACPConfig{Command: "test"})
	events := make(chan StreamEvent, 2)
	var attempts, resets int
	err := transport.streamEventsWithRetry(context.Background(), Request{Prompt: "test"}, events,
		func(context.Context, Request, chan<- StreamEvent) (bool, error) {
			attempts++
			events <- StreamEvent{Type: StreamEventText, Text: "partial"}
			return true, ErrACPConnectionClosed
		},
		func() { resets++ },
	)
	if !errors.Is(err, ErrACPConnectionClosed) {
		t.Fatalf("error = %v, want ErrACPConnectionClosed", err)
	}
	if attempts != 1 || resets != 0 {
		t.Fatalf("attempts=%d resets=%d, want no resend of a delivered prompt", attempts, resets)
	}
}

// A prompt that never reached the agent (the connection closed first) is
// sent again on a fresh connection.
func TestACPTransport_StreamEventsWithRetryRetriesAnUndeliveredPrompt(t *testing.T) {
	transport := NewACPTransport(ACPConfig{Command: "test"})
	events := make(chan StreamEvent, 1)
	var attempts, resets int
	err := transport.streamEventsWithRetry(context.Background(), Request{Prompt: "test"}, events,
		func(context.Context, Request, chan<- StreamEvent) (bool, error) {
			attempts++
			if attempts == 1 {
				return false, ErrACPConnectionClosed
			}
			return false, nil
		},
		func() { resets++ },
	)
	if err != nil {
		t.Fatalf("streamEventsWithRetry returned error: %v", err)
	}
	if attempts != 2 || resets != 1 {
		t.Fatalf("attempts=%d resets=%d, want one retry of the undelivered prompt", attempts, resets)
	}
}

// Agent text that resumes after a tool call must be separated from the
// pre-tool text, otherwise the two blocks glue together
// ("Let me investigate." + "jj does see" -> "Let me investigate.jj does see").
func TestACPTransport_SendStreamingSeparatesTextAcrossToolCalls(t *testing.T) {
	transport, conn, _ := newPipeTransport("test-session")
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Let me investigate."}}}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"v0.7.0: rytzqtsm 045f61f0"}}]}}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"jj does see your tag."}}}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)

	textCh := make(chan string, 10)
	if _, err := transport.sendStreamingAttempt(context.Background(), Request{Prompt: "why"}, textCh); err != nil {
		t.Fatalf("sendStreamingAttempt returned error: %v", err)
	}

	var got strings.Builder
	for {
		select {
		case text := <-textCh:
			got.WriteString(text)
			continue
		default:
		}
		break
	}

	out := got.String()
	if strings.Contains(out, "investigate.jj does see") {
		t.Errorf("text blocks glued across tool call: %q", out)
	}
	if !strings.Contains(out, "Let me investigate.") || !strings.Contains(out, "jj does see your tag.") {
		t.Fatalf("missing expected text blocks: %q", out)
	}
	// The two text blocks must be separated by a blank line (paragraph break).
	if !strings.Contains(out, "Let me investigate.\n\njj does see your tag.") {
		t.Errorf("text blocks not separated by paragraph break: %q", out)
	}
}

func TestACPTransport_SendStreamingAttemptNoTextIncludesStopReason(t *testing.T) {
	transport, conn, _ := newPipeTransport("test-session")
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"max_tokens"}}`)

	textCh := make(chan string, 10)
	receivedText, err := transport.sendStreamingAttempt(context.Background(), Request{Prompt: "hello"}, textCh)
	if err == nil {
		t.Fatal("sendStreamingAttempt returned nil error, want no-output error")
	}
	if receivedText {
		t.Fatal("sendStreamingAttempt receivedText = true, want false")
	}
	if !errors.Is(err, ErrACPNoOutput) {
		t.Fatalf("error = %v, want ErrACPNoOutput", err)
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("error = %v, want stop reason", err)
	}
}

func TestACPTransport_InitializeUsesV1ClientCapabilities(t *testing.T) {
	transport, conn, stdin := newPipeTransport("")
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{},"authMethods":[]}}`)

	if err := transport.initialize(context.Background(), conn); err != nil {
		t.Fatalf("initialize returned error: %v", err)
	}

	stdin.mu.Lock()
	written := string(stdin.written)
	stdin.mu.Unlock()

	if strings.Contains(written, `"capabilities"`) {
		t.Fatalf("initialize request used legacy capabilities field: %s", written)
	}
	if !strings.Contains(written, `"clientCapabilities"`) {
		t.Fatalf("initialize request missing clientCapabilities: %s", written)
	}
	if !strings.Contains(written, `"readTextFile":false`) ||
		!strings.Contains(written, `"writeTextFile":false`) ||
		!strings.Contains(written, `"terminal":false`) {
		t.Fatalf("initialize request should explicitly advertise unsupported client capabilities: %s", written)
	}
}

func TestACPTransport_InitializeParsesLifecycleCapabilities(t *testing.T) {
	transport, conn, _ := newPipeTransport("")
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"close":{},"resume":{},"list":{},"fork":{}}}}}`)

	if err := transport.initialize(context.Background(), conn); err != nil {
		t.Fatalf("initialize returned error: %v", err)
	}

	if !conn.capabilities.LoadSession {
		t.Fatal("LoadSession capability = false, want true")
	}
	if !conn.capabilities.SessionClose {
		t.Fatal("SessionClose capability = false, want true")
	}
	if !conn.capabilities.SessionResume {
		t.Fatal("SessionResume capability = false, want true")
	}
	if !conn.capabilities.SessionList {
		t.Fatal("SessionList capability = false, want true")
	}
	if !conn.capabilities.SessionFork {
		t.Fatal("SessionFork capability = false, want true")
	}
}

func TestACPTransport_InitializeRejectsDeprecatedClaudeCodeACP(t *testing.T) {
	transport, conn, _ := newPipeTransport("")
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{}}},"agentInfo":{"name":"@zed-industries/claude-code-acp","title":"Claude Code","version":"0.12.6"}}}`)

	err := transport.initialize(context.Background(), conn)
	if err == nil {
		t.Fatal("initialize returned nil error, want unsupported agent error")
	}
	if !errors.Is(err, ErrACPUnsupportedAgent) {
		t.Fatalf("error = %v, want ErrACPUnsupportedAgent", err)
	}
	if !strings.Contains(err.Error(), "claude-agent-acp") {
		t.Fatalf("error = %v, want claude-agent-acp migration hint", err)
	}
}

func TestACPTransport_InitializeRejectsUnsupportedProtocolVersion(t *testing.T) {
	transport, conn, _ := newPipeTransport("")
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":2}}`)

	err := transport.initialize(context.Background(), conn)
	if err == nil {
		t.Fatal("initialize returned nil error, want unsupported protocol error")
	}
	if !strings.Contains(err.Error(), "unsupported ACP protocol version 2") {
		t.Fatalf("initialize error = %v, want unsupported protocol version", err)
	}
}

// A canceled turn tells the agent with session/cancel and keeps the
// session; an agent that never answers the prompt is dropped like a lost
// connection, which keeps a resumable session as the resume candidate.
func TestACPTransport_CanceledTurnKeepsSessionAndDropsAnUnresponsiveAgent(t *testing.T) {
	transport, conn, stdin := newPipeTransport("test-session")
	conn.session.cwd = "/tmp/hash-test"
	conn.capabilities = acpCapabilities{SessionResume: true}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	turn := &promptTurn{t: transport, c: conn, ctx: ctx, events: make(chan StreamEvent, 1), sessionID: "test-session", requestID: 7}
	start := time.Now()
	if err := turn.run(); !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v, want context.Canceled", err)
	}
	if waited := time.Since(start); waited < acpCancelTimeout {
		t.Fatalf("returned after %v, before the agent could answer the cancel", waited)
	}

	stdin.mu.Lock()
	written := string(stdin.written)
	stdin.mu.Unlock()
	if !strings.Contains(written, `"method":"session/cancel"`) || !strings.Contains(written, `"sessionId":"test-session"`) {
		t.Fatalf("wrote %q, want a session/cancel for test-session", written)
	}
	if transport.connected() {
		t.Fatal("an agent that ignored the cancel should be dropped")
	}
	if transport.resume == nil || transport.resume.id != "test-session" {
		t.Fatalf("resume = %+v, want test-session kept", transport.resume)
	}
}

func TestACPTransport_ResetConnectionKeepsResumeCandidateWhenSupported(t *testing.T) {
	transport, conn, _ := newPipeTransport("test-session")
	conn.session.cwd = "/tmp/hash-test"
	conn.capabilities = acpCapabilities{
		SessionResume: true,
	}

	transport.resetConnection()

	if transport.resume == nil || *transport.resume != (acpSession{id: "test-session", cwd: "/tmp/hash-test"}) {
		t.Fatalf("resume = %+v, want test-session in /tmp/hash-test", transport.resume)
	}
	if transport.connected() {
		t.Fatal("connection should be dropped")
	}
}

func TestACPTransport_SendStreamingAttemptResumesBeforeNewSession(t *testing.T) {
	transport, conn, stdin := newPipeTransport("")
	conn.capabilities = acpCapabilities{
		SessionResume: true,
	}
	transport.resume = &acpSession{id: "previous-session", cwd: "/tmp/hash-test"}
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"previous-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"resumed"}}}}`)
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":2,"result":{"stopReason":"end_turn"}}`)

	textCh := make(chan string, 10)
	receivedText, err := transport.sendStreamingAttempt(context.Background(), Request{
		Prompt: "continue",
		Context: Context{
			Cwd: "/tmp/hash-test",
		},
	}, textCh)
	if err != nil {
		t.Fatalf("sendStreamingAttempt returned error: %v", err)
	}
	if !receivedText {
		t.Fatal("receivedText = false, want true")
	}
	if conn.session == nil || conn.session.id != "previous-session" {
		t.Fatalf("session = %+v, want previous-session", conn.session)
	}
	if transport.resume != nil {
		t.Fatalf("resume = %+v, want cleared", transport.resume)
	}

	stdin.mu.Lock()
	written := string(stdin.written)
	stdin.mu.Unlock()

	if !strings.Contains(written, `"method":"session/resume"`) {
		t.Fatalf("written RPCs missing session/resume: %s", written)
	}
	if strings.Contains(written, `"method":"session/new"`) {
		t.Fatalf("written RPCs should not create a new session after resume: %s", written)
	}
}

func TestACPTransport_EnsureSessionKeepsResumeCandidateWhenResumeConnectionDrops(t *testing.T) {
	transport, conn, _ := newPipeTransport("")
	conn.capabilities = acpCapabilities{
		SessionResume: true,
	}
	transport.resume = &acpSession{id: "previous-session", cwd: "/tmp/hash-test"}
	conn.messages = make(chan []byte)
	close(conn.messages)

	sessionID, err := transport.ensureSession(context.Background(), "/tmp/hash-test")
	if err == nil {
		t.Fatal("ensureSession returned nil error, want retryable connection error")
	}
	if sessionID != "" {
		t.Fatalf("sessionID = %q, want empty on failed resume", sessionID)
	}
	if !errors.Is(err, ErrACPConnectionClosed) {
		t.Fatalf("error = %v, want ErrACPConnectionClosed", err)
	}
	if transport.resume == nil || transport.resume.id != "previous-session" {
		t.Fatalf("resume = %+v, want preserved previous-session", transport.resume)
	}
}

func TestACPTransport_EnsureSessionClearsResumeCandidateWhenResumeUnsupported(t *testing.T) {
	transport, conn, _ := newPipeTransport("")
	transport.resume = &acpSession{id: "previous-session", cwd: "/tmp/hash-test"}
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"fresh-session"}}`)

	sessionID, err := transport.ensureSession(context.Background(), "/tmp/hash-test")
	if err != nil {
		t.Fatalf("ensureSession returned error: %v", err)
	}
	if sessionID != "fresh-session" {
		t.Fatalf("sessionID = %q, want fresh-session", sessionID)
	}
	if transport.resume != nil {
		t.Fatalf("resume = %+v, want cleared", transport.resume)
	}
}

func TestACPTransport_NewSessionSendsAbsoluteCWD(t *testing.T) {
	transport, conn, stdin := newPipeTransport("")
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"test-session"}}`)

	sessionID, _, err := transport.newSession(context.Background(), ".")
	if err != nil {
		t.Fatalf("newSession returned error: %v", err)
	}
	if sessionID != "test-session" {
		t.Fatalf("sessionID = %q, want test-session", sessionID)
	}

	stdin.mu.Lock()
	written := append([]byte(nil), stdin.written...)
	stdin.mu.Unlock()

	var req struct {
		Params newSessionParams `json:"params"`
	}
	if err := json.Unmarshal(written, &req); err != nil {
		t.Fatalf("unmarshal new session request: %v\n%s", err, string(written))
	}
	if !filepath.IsAbs(req.Params.Cwd) {
		t.Fatalf("session cwd = %q, want absolute path", req.Params.Cwd)
	}
}

// When the agent ends its turn on a tool's output, that output stands in for
// the reply; tool output the agent already answered is not shown again.
func TestACPTransport_ToolFallbackShowsOnlyUnansweredToolText(t *testing.T) {
	transport, conn, _ := newPipeTransport("test-session")
	toolUpdate := func(id, text string) []byte {
		return []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"completed","content":[{"type":"content","content":{"type":"text","text":"` + text + `"}}]}}}`)
	}
	conn.messages <- toolUpdate("call-1", "first output")
	conn.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Read it."}}}}`)
	conn.messages <- toolUpdate("call-2", "second output")
	conn.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)

	textCh := make(chan string, 10)
	if _, err := transport.sendStreamingAttempt(context.Background(), Request{Prompt: "x"}, textCh); err != nil {
		t.Fatalf("sendStreamingAttempt: %v", err)
	}
	close(textCh)
	var got strings.Builder
	for text := range textCh {
		got.WriteString(text)
	}
	if got.String() != "Read it.\nsecond output" {
		t.Fatalf("reply = %q, want %q", got.String(), "Read it.\nsecond output")
	}
}
