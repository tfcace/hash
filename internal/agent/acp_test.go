package agent

import (
	"bufio"
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
	result := make(chan struct {
		observed bool
		err      error
	}, 1)
	go func() {
		observed, err := t.sendEventStreamingAttempt(ctx, req, events)
		close(events)
		result <- struct {
			observed bool
			err      error
		}{observed, err}
	}()

	receivedText := false
	for event := range events {
		if event.Type == StreamEventText && event.Text != "" {
			textCh <- event.Text
			receivedText = true
		}
	}
	outcome := <-result
	return receivedText || outcome.observed, outcome.err
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

func TestACPTransport_ReadLoopClosesOnlyOriginalChannels(t *testing.T) {
	transport := NewACPTransport(ACPConfig{Command: "test"})

	pr, pw := io.Pipe()
	reader := bufio.NewReader(pr)
	originalMessages := make(chan []byte, 1)
	originalDone := make(chan struct{})
	replacementMessages := make(chan []byte, 1)
	replacementDone := make(chan struct{})

	transport.reader = reader
	transport.messages = replacementMessages
	transport.done = replacementDone

	go transport.readLoop(reader, originalMessages, originalDone)

	// Simulate connection reset while the old read loop is still running.
	transport.reader = nil
	transport.messages = replacementMessages
	transport.done = replacementDone

	if _, err := pw.Write([]byte("{\"jsonrpc\":\"2.0\"}\n")); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}

	select {
	case <-originalDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for original read loop to exit")
	}

	select {
	case msg, ok := <-originalMessages:
		if !ok {
			t.Fatal("original messages channel closed before delivering buffered message")
		}
		if string(msg) != "{\"jsonrpc\":\"2.0\"}\n" {
			t.Fatalf("unexpected message %q", string(msg))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for original message")
	}

	select {
	case _, ok := <-originalMessages:
		if ok {
			t.Fatal("expected original messages channel to be closed after draining")
		}
	default:
		t.Fatal("expected original messages channel to be closed")
	}

	select {
	case <-replacementDone:
		t.Fatal("replacement done channel should stay open")
	default:
	}

	select {
	case _, ok := <-replacementMessages:
		if !ok {
			t.Fatal("replacement messages channel should stay open")
		}
		t.Fatal("replacement messages channel should remain unused")
	default:
	}
}

func TestACPTransport_SendStreamingAttemptEmitsCompletedToolCallContent(t *testing.T) {
	transport := &ACPTransport{
		config:    ACPConfig{Command: "test"},
		stdin:     newMockPipe(),
		sessionID: "test-session",
		messages:  make(chan []byte, 10),
		done:      make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"docker-desktop\nkind-kind"}}]}}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)

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
	transport := &ACPTransport{
		config:    ACPConfig{Command: "test"},
		stdin:     newMockPipe(),
		sessionID: "test-session",
		messages:  make(chan []byte, 10),
		done:      make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"pwd","kind":"execute","status":"pending"}}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed"}}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Done."}}}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)

	events := make(chan StreamEvent, 10)
	observed, err := transport.sendEventStreamingAttempt(context.Background(), Request{Prompt: "test"}, events)
	if err != nil {
		t.Fatalf("sendEventStreamingAttempt returned error: %v", err)
	}
	if !observed {
		t.Fatal("tool lifecycle should count as observed activity")
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

func TestACPTransport_SendEventStreamingAttemptTreatsTextAsObservedOutput(t *testing.T) {
	transport := &ACPTransport{
		config:    ACPConfig{Command: "test"},
		stdin:     newMockPipe(),
		sessionID: "test-session",
		messages:  make(chan []byte, 10),
		done:      make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"partial"}}}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)

	events := make(chan StreamEvent, 2)
	observed, err := transport.sendEventStreamingAttempt(context.Background(), Request{Prompt: "test"}, events)
	if err != nil {
		t.Fatalf("sendEventStreamingAttempt returned error: %v", err)
	}
	if !observed {
		t.Fatal("assistant text should count as observed output and suppress a retry")
	}
}

func TestACPTransport_StreamEventsWithRetryDoesNotReplayVisibleOutput(t *testing.T) {
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
		t.Fatalf("attempts=%d resets=%d, want no replay after text", attempts, resets)
	}
}

func TestACPTransport_StreamEventsWithRetryRetriesBeforeVisibleOutput(t *testing.T) {
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
		t.Fatalf("attempts=%d resets=%d, want one retry before output", attempts, resets)
	}
}

// Agent text that resumes after a tool call must be separated from the
// pre-tool text, otherwise the two blocks glue together
// ("Let me investigate." + "jj does see" -> "Let me investigate.jj does see").
func TestACPTransport_SendStreamingSeparatesTextAcrossToolCalls(t *testing.T) {
	transport := &ACPTransport{
		config:    ACPConfig{Command: "test"},
		stdin:     newMockPipe(),
		sessionID: "test-session",
		messages:  make(chan []byte, 10),
		done:      make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Let me investigate."}}}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"v0.7.0: rytzqtsm 045f61f0"}}]}}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"test-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"jj does see your tag."}}}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)

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
	transport := &ACPTransport{
		config:    ACPConfig{Command: "test"},
		stdin:     newMockPipe(),
		sessionID: "test-session",
		messages:  make(chan []byte, 10),
		done:      make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"max_tokens"}}`)

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
	stdin := newMockPipe()
	transport := &ACPTransport{
		config:   ACPConfig{Command: "test"},
		stdin:    stdin,
		messages: make(chan []byte, 10),
		done:     make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{},"authMethods":[]}}`)

	if err := transport.initialize(context.Background()); err != nil {
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
	transport := &ACPTransport{
		config:   ACPConfig{Command: "test"},
		stdin:    newMockPipe(),
		messages: make(chan []byte, 10),
		done:     make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"close":{},"resume":{},"list":{},"fork":{}}}}}`)

	if err := transport.initialize(context.Background()); err != nil {
		t.Fatalf("initialize returned error: %v", err)
	}

	if !transport.capabilities.LoadSession {
		t.Fatal("LoadSession capability = false, want true")
	}
	if !transport.capabilities.SessionClose {
		t.Fatal("SessionClose capability = false, want true")
	}
	if !transport.capabilities.SessionResume {
		t.Fatal("SessionResume capability = false, want true")
	}
	if !transport.capabilities.SessionList {
		t.Fatal("SessionList capability = false, want true")
	}
	if !transport.capabilities.SessionFork {
		t.Fatal("SessionFork capability = false, want true")
	}
}

func TestACPTransport_InitializeRejectsDeprecatedClaudeCodeACP(t *testing.T) {
	transport := &ACPTransport{
		config:   ACPConfig{Command: "test"},
		stdin:    newMockPipe(),
		messages: make(chan []byte, 10),
		done:     make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{}}},"agentInfo":{"name":"@zed-industries/claude-code-acp","title":"Claude Code","version":"0.12.6"}}}`)

	err := transport.initialize(context.Background())
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
	transport := &ACPTransport{
		config:   ACPConfig{Command: "test"},
		stdin:    newMockPipe(),
		messages: make(chan []byte, 10),
		done:     make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":2}}`)

	err := transport.initialize(context.Background())
	if err == nil {
		t.Fatal("initialize returned nil error, want unsupported protocol error")
	}
	if !strings.Contains(err.Error(), "unsupported ACP protocol version 2") {
		t.Fatalf("initialize error = %v, want unsupported protocol version", err)
	}
}

func TestACPTransport_CloseCurrentSessionUsesSessionCloseWhenSupported(t *testing.T) {
	stdin := newMockPipe()
	transport := &ACPTransport{
		config:     ACPConfig{Command: "test"},
		stdin:      stdin,
		sessionID:  "test-session",
		sessionCWD: "/tmp/hash-test",
		capabilities: acpCapabilities{
			SessionClose: true,
		},
		messages: make(chan []byte, 10),
		done:     make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)

	transport.closeCurrentSession()

	if transport.sessionID != "" {
		t.Fatalf("sessionID = %q, want cleared", transport.sessionID)
	}
	if transport.sessionCWD != "" {
		t.Fatalf("sessionCWD = %q, want cleared", transport.sessionCWD)
	}

	stdin.mu.Lock()
	written := append([]byte(nil), stdin.written...)
	stdin.mu.Unlock()

	var req struct {
		Method string `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
		} `json:"params"`
	}
	if err := json.Unmarshal(written, &req); err != nil {
		t.Fatalf("unmarshal close request: %v\n%s", err, string(written))
	}
	if req.Method != "session/close" {
		t.Fatalf("method = %q, want session/close", req.Method)
	}
	if req.Params.SessionID != "test-session" {
		t.Fatalf("sessionId = %q, want test-session", req.Params.SessionID)
	}
}

func TestACPTransport_CloseCurrentSessionFallsBackToCancelWhenUnsupported(t *testing.T) {
	stdin := newMockPipe()
	transport := &ACPTransport{
		config:     ACPConfig{Command: "test"},
		stdin:      stdin,
		sessionID:  "test-session",
		sessionCWD: "/tmp/hash-test",
		messages:   make(chan []byte, 10),
		done:       make(chan struct{}),
	}

	transport.closeCurrentSession()

	stdin.mu.Lock()
	written := append([]byte(nil), stdin.written...)
	stdin.mu.Unlock()

	var notification struct {
		Method string `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
		} `json:"params"`
	}
	if err := json.Unmarshal(written, &notification); err != nil {
		t.Fatalf("unmarshal cancel notification: %v\n%s", err, string(written))
	}
	if notification.Method != "session/cancel" {
		t.Fatalf("method = %q, want session/cancel", notification.Method)
	}
	if notification.Params.SessionID != "test-session" {
		t.Fatalf("sessionId = %q, want test-session", notification.Params.SessionID)
	}
}

func TestACPTransport_ResetConnectionKeepsResumeCandidateWhenSupported(t *testing.T) {
	transport := &ACPTransport{
		config:     ACPConfig{Command: "test"},
		stdin:      newMockPipe(),
		sessionID:  "test-session",
		sessionCWD: "/tmp/hash-test",
		capabilities: acpCapabilities{
			SessionResume: true,
		},
		messages: make(chan []byte, 10),
		done:     make(chan struct{}),
	}

	transport.resetConnection()

	if transport.resumeSessionID != "test-session" {
		t.Fatalf("resumeSessionID = %q, want test-session", transport.resumeSessionID)
	}
	if transport.resumeSessionCWD != "/tmp/hash-test" {
		t.Fatalf("resumeSessionCWD = %q, want /tmp/hash-test", transport.resumeSessionCWD)
	}
	if transport.sessionID != "" {
		t.Fatalf("sessionID = %q, want cleared", transport.sessionID)
	}
}

func TestACPTransport_SendStreamingAttemptResumesBeforeNewSession(t *testing.T) {
	stdin := newMockPipe()
	transport := &ACPTransport{
		config:           ACPConfig{Command: "test"},
		stdin:            stdin,
		resumeSessionID:  "previous-session",
		resumeSessionCWD: "/tmp/hash-test",
		capabilities: acpCapabilities{
			SessionResume: true,
		},
		messages: make(chan []byte, 10),
		done:     make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"previous-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"resumed"}}}}`)
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":2,"result":{"stopReason":"end_turn"}}`)

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
	if transport.sessionID != "previous-session" {
		t.Fatalf("sessionID = %q, want previous-session", transport.sessionID)
	}
	if transport.resumeSessionID != "" {
		t.Fatalf("resumeSessionID = %q, want cleared", transport.resumeSessionID)
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
	transport := &ACPTransport{
		config:           ACPConfig{Command: "test"},
		stdin:            newMockPipe(),
		resumeSessionID:  "previous-session",
		resumeSessionCWD: "/tmp/hash-test",
		capabilities: acpCapabilities{
			SessionResume: true,
		},
		messages: make(chan []byte),
		done:     make(chan struct{}),
	}
	close(transport.messages)

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
	if transport.resumeSessionID != "previous-session" {
		t.Fatalf("resumeSessionID = %q, want preserved previous-session", transport.resumeSessionID)
	}
}

func TestACPTransport_EnsureSessionClearsResumeCandidateWhenResumeUnsupported(t *testing.T) {
	transport := &ACPTransport{
		config:           ACPConfig{Command: "test"},
		stdin:            newMockPipe(),
		resumeSessionID:  "previous-session",
		resumeSessionCWD: "/tmp/hash-test",
		messages:         make(chan []byte, 10),
		done:             make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"fresh-session"}}`)

	sessionID, err := transport.ensureSession(context.Background(), "/tmp/hash-test")
	if err != nil {
		t.Fatalf("ensureSession returned error: %v", err)
	}
	if sessionID != "fresh-session" {
		t.Fatalf("sessionID = %q, want fresh-session", sessionID)
	}
	if transport.resumeSessionID != "" {
		t.Fatalf("resumeSessionID = %q, want cleared", transport.resumeSessionID)
	}
}

func TestACPTransport_NewSessionSendsAbsoluteCWD(t *testing.T) {
	stdin := newMockPipe()
	transport := &ACPTransport{
		config:   ACPConfig{Command: "test"},
		stdin:    stdin,
		messages: make(chan []byte, 10),
		done:     make(chan struct{}),
	}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"test-session"}}`)

	sessionID, err := transport.newSession(context.Background(), ".")
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
