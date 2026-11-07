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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The fake ACP agent runs as a child process of the test binary so lifecycle
// tests exercise the real spawn, pipe and reconnect paths.
const fakeAgentEnv = "HASH_FAKE_ACP_AGENT"

// fakeAgentModeEnv selects a scripted misbehavior for one test:
//
//	slow                  pause between the two prompt chunks
//	chunks                stream 200 chunks per prompt
//	stale-permission      ask permission for a session that is no longer current
//	permission-then-crash ask permission for the current session, then exit
//	request-during-new    ask the client something before answering session/new
//	resume                advertise session/resume; resumed sessions start on the default model
//	resume-hang           advertise session/resume; reply with the session id, and never answer a process's second prompt
//	cwd                   reply with the session's cwd
//	cancelable            reply with the session id; the first prompt waits for session/cancel, then sends a late
//	                      chunk and stops as canceled; set_config_option answers after 500ms
//	slow-start            pause before the first prompt chunk
//	pgid                  reply with the agent's process group and a child process's pid
//	silent                answer every prompt with no text
//	flood                 the first prompt streams "old-N" chunks until session/cancel, then stops as canceled;
//	                      later prompts reply "cancels=<n>"
//	stdout-ends           answer the first prompt, then close stdout and keep reading stdin (alive, deaf)
const fakeAgentModeEnv = "HASH_FAKE_ACP_MODE"

func TestFakeACPAgent(t *testing.T) {
	if os.Getenv(fakeAgentEnv) != "1" {
		t.Skip("helper process for ACP lifecycle tests")
	}
	runFakeACPAgent(os.Stdin, os.Stdout, os.Getenv(fakeAgentModeEnv))
	os.Exit(0)
}

// newFakeAgentTransport returns a transport whose agent is the fake above.
func newFakeAgentTransport(t *testing.T, mode string) *ACPTransport {
	t.Helper()
	t.Setenv(fakeAgentEnv, "1")
	t.Setenv(fakeAgentModeEnv, mode)
	transport := NewACPTransport(ACPConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestFakeACPAgent$"},
	})
	t.Cleanup(func() { _ = transport.Close() })
	return transport
}

type fakeRPC struct {
	ID     *int64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

type fakeAgent struct {
	mode    string
	out     io.Writer
	writeMu sync.Mutex

	mu            sync.Mutex
	sessions      map[string]*fakeSession // by id
	count         int
	prompts       int
	nextID        int64
	waiting       map[int64]chan json.RawMessage // client replies to our requests
	cancels       map[string]chan struct{}       // closed when the client cancels the session's turn
	canceledTurns int                            // session/cancel notifications received
}

type fakeSession struct {
	cwd   string
	model string
}

func runFakeACPAgent(in io.Reader, out io.Writer, mode string) {
	a := &fakeAgent{
		mode:     mode,
		out:      out,
		sessions: map[string]*fakeSession{},
		nextID:   1000,
		waiting:  map[int64]chan json.RawMessage{},
		cancels:  map[string]chan struct{}{},
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var msg fakeRPC
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		if msg.ID == nil {
			if msg.Method == "session/cancel" {
				var p struct {
					SessionID string `json:"sessionId"`
				}
				_ = json.Unmarshal(msg.Params, &p)
				a.mu.Lock()
				a.canceledTurns++
				a.mu.Unlock()
				a.cancel(p.SessionID)
			}
			continue
		}
		if msg.Method == "" {
			a.mu.Lock()
			ch := a.waiting[*msg.ID]
			delete(a.waiting, *msg.ID)
			a.mu.Unlock()
			if ch != nil {
				ch <- msg.Result
			}
			continue
		}
		go a.handle(*msg.ID, msg.Method, msg.Params)
	}
}

func (a *fakeAgent) send(v any) {
	data, _ := json.Marshal(v)
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_, _ = a.out.Write(append(data, '\n'))
}

func (a *fakeAgent) reply(id int64, result any) {
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// ask sends a request to the client and waits for its reply.
func (a *fakeAgent) ask(method string, params any) json.RawMessage {
	ch := make(chan json.RawMessage, 1)
	a.mu.Lock()
	a.nextID++
	id := a.nextID
	a.waiting[id] = ch
	a.mu.Unlock()
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return <-ch
}

func (a *fakeAgent) chunk(sessionID, text string) {
	a.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
		"sessionId": sessionID,
		"update":    map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}},
	}})
}

func fakeModelOptions(current string) []map[string]any {
	return []map[string]any{{
		"id": "model", "category": "model", "currentValue": current,
		"options": []map[string]any{{"value": "default", "name": "Default"}, {"value": "sonnet", "name": "Sonnet"}},
	}}
}

func fakePermissionParams(sessionID string) map[string]any {
	return map[string]any{
		"sessionId": sessionID,
		"toolCall":  map[string]any{"toolCallId": "call-1", "title": "rm -rf build", "kind": "execute"},
		"options": []map[string]any{
			{"kind": "allow_once", "name": "Allow", "optionId": "allow"},
			{"kind": "reject_once", "name": "Reject", "optionId": "reject"},
		},
	}
}

// canceled returns the channel closed when the session's turn is canceled.
func (a *fakeAgent) canceled(sessionID string) chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch, ok := a.cancels[sessionID]
	if !ok {
		ch = make(chan struct{})
		a.cancels[sessionID] = ch
	}
	return ch
}

func (a *fakeAgent) cancel(sessionID string) {
	ch := a.canceled(sessionID)
	a.mu.Lock()
	defer a.mu.Unlock()
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func (a *fakeAgent) session(id string) *fakeSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *fakeAgent) handle(id int64, method string, params json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
		Value     string `json:"value"`
	}
	_ = json.Unmarshal(params, &p)
	switch method {
	case "initialize":
		caps := map[string]any{}
		if a.mode == "resume" || a.mode == "resume-hang" {
			caps["sessionCapabilities"] = map[string]any{"resume": map[string]any{}}
		}
		a.reply(id, map[string]any{"protocolVersion": 1, "agentCapabilities": caps})
	case "session/new":
		if a.mode == "request-during-new" {
			a.ask("fs/read_text_file", map[string]any{"path": "/etc/hosts"})
		}
		a.mu.Lock()
		a.count++
		sid := fmt.Sprintf("fake-%d-%d", os.Getpid(), a.count)
		a.sessions[sid] = &fakeSession{cwd: p.Cwd, model: "default"}
		a.mu.Unlock()
		a.reply(id, map[string]any{"sessionId": sid, "configOptions": fakeModelOptions("default")})
	case "session/resume":
		// A resumed session in a fresh process comes back on the default model.
		a.mu.Lock()
		a.sessions[p.SessionID] = &fakeSession{cwd: p.Cwd, model: "default"}
		a.mu.Unlock()
		a.reply(id, map[string]any{"configOptions": fakeModelOptions("default")})
	case "session/set_config_option":
		if a.mode == "cancelable" {
			time.Sleep(500 * time.Millisecond)
		}
		if s := a.session(p.SessionID); s != nil {
			a.mu.Lock()
			s.model = p.Value
			a.mu.Unlock()
		}
		a.reply(id, map[string]any{"configOptions": fakeModelOptions(p.Value)})
	case "session/prompt":
		a.prompt(id, p.SessionID)
	default:
		a.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "Method not found"}})
	}
}

func (a *fakeAgent) prompt(id int64, sessionID string) {
	switch a.mode {
	case "stale-permission":
		a.ask("session/request_permission", fakePermissionParams("long-gone-session"))
	case "permission-then-crash":
		a.ask("session/request_permission", fakePermissionParams(sessionID))
		os.Exit(1)
	case "chunks":
		for i := 0; i < 200; i++ {
			a.chunk(sessionID, "x")
		}
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	case "resume":
		s := a.session(sessionID)
		a.mu.Lock()
		model := s.model
		a.mu.Unlock()
		a.chunk(sessionID, "model="+model)
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	case "cancelable":
		a.mu.Lock()
		a.prompts++
		n := a.prompts
		a.mu.Unlock()
		a.chunk(sessionID, "sid="+sessionID)
		if n == 1 {
			select {
			case <-a.canceled(sessionID):
			case <-time.After(5 * time.Second):
			}
			a.chunk(sessionID, " late")
			a.reply(id, map[string]any{"stopReason": "cancelled"}) //nolint:misspell // ACP wire value
			return
		}
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	case "flood":
		a.flood(id, sessionID)
		return
	case "silent":
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	case "resume-hang":
		a.mu.Lock()
		a.prompts++
		n := a.prompts
		a.mu.Unlock()
		if n == 2 {
			return // hang: never answer
		}
		a.chunk(sessionID, "sid="+sessionID)
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	case "cwd":
		a.chunk(sessionID, "cwd="+a.session(sessionID).cwd)
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	case "stdout-ends":
		a.chunk(sessionID, "one two")
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		a.writeMu.Lock()
		_ = os.Stdout.Close()
		a.writeMu.Unlock()
		return
	case "pgid":
		child := exec.Command("sleep", "60")
		_ = child.Start()
		a.chunk(sessionID, fmt.Sprintf("pgid=%d child=%d", syscall.Getpgrp(), child.Process.Pid))
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	}
	if a.mode == "slow-start" {
		time.Sleep(400 * time.Millisecond)
	}
	a.chunk(sessionID, "one ")
	if a.mode == "slow" {
		time.Sleep(300 * time.Millisecond)
	}
	a.chunk(sessionID, "two")
	a.reply(id, map[string]any{"stopReason": "end_turn"})
}

// flood streams the first prompt's reply as fast as it can until the client
// cancels it; later prompts report how many cancels arrived.
func (a *fakeAgent) flood(id int64, sessionID string) {
	a.mu.Lock()
	a.prompts++
	n := a.prompts
	a.mu.Unlock()
	if n > 1 {
		a.mu.Lock()
		c := a.canceledTurns
		a.mu.Unlock()
		a.chunk(sessionID, fmt.Sprintf("cancels=%d", c))
		a.reply(id, map[string]any{"stopReason": "end_turn"})
		return
	}
	canceled := a.canceled(sessionID)
	deadline := time.After(5 * time.Second)
	for i := 0; ; i++ {
		select {
		case <-canceled:
			a.reply(id, map[string]any{"stopReason": "cancelled"}) //nolint:misspell // ACP wire value
			return
		case <-deadline:
			a.reply(id, map[string]any{"stopReason": "end_turn"})
			return
		default:
		}
		a.chunk(sessionID, fmt.Sprintf("old-%d ", i))
	}
}

func askFake(t *testing.T, transport *ACPTransport) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := NewClient(transport).Ask(ctx, Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return strings.TrimSpace(resp.Explanation + resp.Command)
}

// Restart after `agent update` closes the transport and reconnects it. The
// second connection used to reuse channels the first read loop had already
// closed, which panicked ("send on closed channel" / "close of closed channel").
func TestACPTransport_ReconnectAfterCloseDoesNotPanic(t *testing.T) {
	transport := newFakeAgentTransport(t, "")

	if got := askFake(t, transport); got != "one two" {
		t.Fatalf("first reply = %q, want %q", got, "one two")
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Give the first read loop time to observe EOF and close its channels.
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := transport.Restart(ctx); err != nil {
		t.Fatalf("Restart after Close: %v", err)
	}
	if got := askFake(t, transport); got != "one two" {
		t.Fatalf("reply after reconnect = %q, want %q", got, "one two")
	}
}

// A model switch while a prompt turn is streaming used to read the same reply
// channel as the turn: each side dropped messages meant for the other, so the
// turn lost text or SetModel waited for a reply the turn had swallowed.
func TestACPTransport_ConcurrentCallersDoNotStealReplies(t *testing.T) {
	transport := newFakeAgentTransport(t, "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := transport.EnsureModelInfo(ctx); err != nil {
		t.Fatalf("EnsureModelInfo: %v", err)
	}

	for round := 0; round < 3; round++ {
		textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "hi"})
		var got strings.Builder
		first, ok := <-textCh
		if !ok {
			t.Fatalf("round %d: stream closed before first chunk: %v", round, <-errCh)
		}
		got.WriteString(first)

		setErrs := make(chan error, 1)
		go func() {
			setCtx, setCancel := context.WithTimeout(ctx, 3*time.Second)
			defer setCancel()
			setErrs <- transport.SetModel(setCtx, "sonnet")
		}()

		for chunk := range textCh {
			got.WriteString(chunk)
		}
		for err := range errCh {
			if err != nil {
				t.Fatalf("round %d: stream error: %v", round, err)
			}
		}
		if got.String() != "one two" {
			t.Fatalf("round %d: turn text = %q, want %q", round, got.String(), "one two")
		}
		if err := <-setErrs; err != nil {
			t.Fatalf("round %d: SetModel during turn: %v", round, err)
		}
	}
}

// A permission request that names a session other than the turn's (left over
// from a canceled turn) must not prompt the user; it gets the canceled outcome.
func TestACPTransport_StalePermissionRequestIsCancelledNotPrompted(t *testing.T) {
	transport := newFakeAgentTransport(t, "stale-permission")
	var prompts atomic.Int32
	transport.SetPermissionHandler(func(context.Context, ToolPermissionRequest) (bool, bool) {
		prompts.Add(1)
		return true, false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "hi"})
	for range textCh {
	}
	for range errCh {
	}
	if n := prompts.Load(); n != 0 {
		t.Fatalf("permission handler called %d times for a stale session, want 0", n)
	}
}

// Once the user has answered a permission prompt the turn has had a visible
// effect; a dropped connection must not replay the prompt and ask again.
func TestACPTransport_AnsweredPermissionPreventsReplay(t *testing.T) {
	transport := newFakeAgentTransport(t, "permission-then-crash")
	var prompts atomic.Int32
	transport.SetPermissionHandler(func(context.Context, ToolPermissionRequest) (bool, bool) {
		prompts.Add(1)
		return true, false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "hi"})
	for range textCh {
	}
	var gotErr error
	for err := range errCh {
		gotErr = err
	}
	if gotErr == nil {
		t.Fatal("expected an error after the agent crashed")
	}
	if n := prompts.Load(); n != 1 {
		t.Fatalf("permission handler called %d times, want 1 (no replay)", n)
	}
}

// An agent request that arrives while hash waits for a plain RPC reply (here
// during session/new) must be answered, or the agent waits forever.
func TestACPTransport_AgentRequestDuringRPCIsAnswered(t *testing.T) {
	transport := newFakeAgentTransport(t, "request-during-new")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := transport.EnsureModelInfo(ctx); err != nil {
		t.Fatalf("EnsureModelInfo: %v", err)
	}
}

// SendStreaming's adapter must not block forever on a reader that stopped
// reading once the request context is done.
func TestACPTransport_SendStreamingAdapterExitsWhenReaderStops(t *testing.T) {
	transport := newFakeAgentTransport(t, "chunks")
	ctx, cancel := context.WithCancel(context.Background())
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "hi"})
	<-textCh // the stream is live; now stop reading text
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-drain(errCh):
	case <-time.After(2 * time.Second):
		t.Fatal("SendStreaming adapter still running after cancel with an idle reader")
	}
}

func drain(errCh <-chan error) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		for range errCh {
		}
		close(done)
	}()
	return done
}

// A resumed session reports its own model; the user's pin is compared against
// that, not against the cache left by the previous connection.
func TestACPTransport_ResumeReappliesPinAgainstResumedModel(t *testing.T) {
	transport := newFakeAgentTransport(t, "resume")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := transport.SetModel(ctx, "sonnet"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if got := askFake(t, transport); got != "model=sonnet" {
		t.Fatalf("before reconnect: %q, want model=sonnet", got)
	}
	transport.resetConnection() // agent hung or died; next turn resumes in a new process
	if got := askFake(t, transport); got != "model=sonnet" {
		t.Fatalf("after resume: %q, want model=sonnet (pin re-applied)", got)
	}
}

// Decision: a cd does not open a new agent session (that would drop the
// conversation). The session keeps the directory it was opened in, and each
// prompt names the shell's current working directory instead.
func TestACPTransport_SessionKeepsOpeningDirectoryAcrossCd(t *testing.T) {
	transport := newFakeAgentTransport(t, "cwd")
	first, second := t.TempDir(), t.TempDir()
	ask := func(cwd string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := NewClient(transport).Ask(ctx, Request{Prompt: "where", Context: Context{Cwd: cwd}})
		if err != nil {
			t.Fatalf("Ask: %v", err)
		}
		return strings.TrimSpace(resp.Explanation + resp.Command)
	}
	if got := ask(first); got != "cwd="+first {
		t.Fatalf("first turn = %q, want cwd=%s", got, first)
	}
	if got := ask(second); got != "cwd="+first {
		t.Fatalf("after cd = %q, want the same session (cwd=%s)", got, first)
	}
	prompt := buildPromptWithContext(Request{Prompt: "where", Context: Context{Cwd: second}})
	if !strings.Contains(prompt, "Working directory: "+second) {
		t.Fatalf("prompt does not name the current directory:\n%s", prompt)
	}
}

// The adapter runs in its own process group: the terminal's Ctrl+C (SIGINT
// to the foreground group) never reaches it, and Close ends the whole group,
// so the adapter's own children are not orphaned.
func TestACPTransport_AdapterRunsInItsOwnProcessGroup(t *testing.T) {
	transport := newFakeAgentTransport(t, "pgid")
	var pgid, child int
	if _, err := fmt.Sscanf(askFake(t, transport), "pgid=%d child=%d", &pgid, &child); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if pgid == syscall.Getpgrp() {
		t.Fatalf("adapter pgid %d is hash's own group: Ctrl+C reaches it", pgid)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(child, syscall.SIGKILL)
			t.Fatalf("adapter child %d survived Close", child)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Close is final: a turn in flight when hash closes the transport ends with
// a non-retryable error, and nothing spawns a new adapter behind hash's back.
// Restart is the one way back.
func TestACPTransport_CloseMidTurnDoesNotReconnect(t *testing.T) {
	transport := newFakeAgentTransport(t, "slow-start")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := transport.EnsureModelInfo(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := NewClient(transport).Ask(ctx, Request{Prompt: "hi"})
		done <- err
	}()
	time.Sleep(150 * time.Millisecond)
	_ = transport.Close()
	err := <-done
	if transport.connected() {
		t.Fatal("the turn reconnected after Close")
	}
	if err == nil || IsRetryableError(err) {
		t.Fatalf("turn error after Close = %v, want a non-retryable error", err)
	}
	if err := transport.EnsureModelInfo(ctx); err == nil || transport.connected() {
		t.Fatalf("EnsureModelInfo after Close = %v (connected=%v), want refused", err, transport.connected())
	}
	if err := transport.Restart(ctx); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if got := askFake(t, transport); got != "one two" {
		t.Fatalf("reply after Restart = %q", got)
	}
}

// An idle timeout ends a hung process, not the conversation. The turn fails
// (the agent had the prompt: a resend would put it in the conversation
// twice) and the next one resumes the same session in a new process.
func TestACPTransport_IdleTimeoutKeepsTheSessionForResume(t *testing.T) {
	transport := newFakeAgentTransport(t, "resume-hang")
	transport.idle = 300 * time.Millisecond
	ask := func() (string, error) { // no deadline: a deadline stretches the idle timeout
		resp, err := NewClient(transport).Ask(context.Background(), Request{Prompt: "hi"})
		return strings.TrimSpace(resp.Explanation + resp.Command), err
	}
	first, err := ask()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ask(); !errors.Is(err, ErrACPIdleTimeout) {
		t.Fatalf("hung turn: %v, want the idle timeout", err)
	}
	if third, err := ask(); err != nil || third != first {
		t.Fatalf("after the idle timeout the reply came from %q (%v), want the resumed %q", third, err, first)
	}
}

// Decision: a prompt the agent received is never sent again behind the
// user's back. A turn that ends with no output (or times out, or loses the
// process) after the prompt went out fails with an error the user can
// retry; only a prompt that never reached the agent is retried. Retrying a
// silent answer used to kill the healthy process and resend the prompt
// into the resumed session, which then held it twice.
func TestACPTransport_NoOutputIsNotResent(t *testing.T) {
	transport := newFakeAgentTransport(t, "silent")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := transport.EnsureModelInfo(ctx); err != nil {
		t.Fatal(err)
	}
	before := transport.currentConn()
	if _, err := NewClient(transport).Ask(ctx, Request{Prompt: "hi"}); !errors.Is(err, ErrACPNoOutput) {
		t.Fatalf("Ask = %v, want ErrACPNoOutput", err)
	}
	if transport.currentConn() != before {
		t.Fatal("a silent answer replaced the agent process (the prompt was resent)")
	}
}

// Decision: a user cancel (Ctrl+C or Esc during a turn, Ctrl+C during a
// plain request such as a model switch) stops the agent's work and keeps the
// session, so the conversation survives. The turn sends session/cancel and
// waits for the agent to stop, so nothing of it reaches the next turn.
func TestACPTransport_CancelKeepsTheSession(t *testing.T) {
	transport := newFakeAgentTransport(t, "cancelable")
	ctx, cancel := context.WithCancel(context.Background())
	textCh, errCh := transport.SendStreaming(ctx, Request{Prompt: "hi"})
	first, ok := <-textCh
	if !ok {
		t.Fatalf("stream closed before the first chunk: %v", <-errCh)
	}
	cancel()
	<-drain(errCh)

	setCtx, setCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer setCancel()
	if err := transport.SetModel(setCtx, "sonnet"); err == nil {
		t.Fatal("SetModel answered before the agent did")
	}

	if got := askFake(t, transport); got != first {
		t.Fatalf("after cancel the reply is %q, want the same session (%q) and nothing of the canceled turn", got, first)
	}
}

// A cancel while the agent floods the turn with text, by a reader that then
// stops reading at once (as a UI that quit rendering does), must still send
// session/cancel and drain the turn: the stream's buffer is full, so the turn
// used to end in its event send without either, and the canceled turn's text
// became the start of the next reply.
func TestACPTransport_CancelDuringFloodSendsCancel(t *testing.T) {
	for run := 0; run < 10; run++ {
		transport := newFakeAgentTransport(t, "flood")
		ctx, cancel := context.WithCancel(context.Background())
		events, errs := transport.SendEventStream(ctx, Request{Prompt: "hi"})
		n := 0
		for ev := range events {
			if ev.Type == StreamEventText {
				if n++; n == 20 {
					break
				}
			}
		}
		cancel()
		<-errs
		if got := askFake(t, transport); got != "cancels=1" {
			t.Fatalf("run %d: next reply = %.120q, want cancels=1 and nothing of the canceled turn", run, got)
		}
		_ = transport.Close()
	}
}

// Restart (agent update) mid-turn ends the turn for good, as Close does: the
// turn used to see its connection lost after Restart had reopened the link,
// took that for a crash and replayed the prompt into the fresh process.
func TestACPTransport_RestartMidTurnDoesNotReplay(t *testing.T) {
	for run := 0; run < 5; run++ {
		transport := newFakeAgentTransport(t, "slow-start")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := transport.EnsureModelInfo(ctx); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := NewClient(transport).Ask(ctx, Request{Prompt: "hi"})
			done <- err
		}()
		time.Sleep(150 * time.Millisecond)
		if err := transport.Restart(ctx); err != nil {
			t.Fatalf("Restart: %v", err)
		}
		err := <-done
		cancel()
		if err == nil || IsRetryableError(err) {
			t.Fatalf("run %d: turn across a Restart ended with %v, want a non-retryable error (no replay)", run, err)
		}
		_ = transport.Close()
	}
}

// An agent whose output ended between turns (it crashed, or closed stdout
// with a child still holding its stdin) is gone even when the next prompt's
// write succeeds: the turn reconnects first instead of failing on a
// connection that can never answer.
func TestACPTransport_TurnReconnectsWhenOutputAlreadyEnded(t *testing.T) {
	transport := newFakeAgentTransport(t, "stdout-ends")
	if got := askFake(t, transport); got != "one two" {
		t.Fatalf("first reply = %q, want %q", got, "one two")
	}
	// The read loop sees the end of stdout and drops the connection, so a
	// connected link is always a live one.
	deadline := time.Now().Add(5 * time.Second)
	for transport.linkState() != linkDisconnected {
		if time.Now().After(deadline) {
			t.Fatal("the link stayed connected after the agent's output ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := NewClient(transport).Ask(ctx, Request{Prompt: "again"})
	if err != nil {
		t.Fatalf("second turn: %v, want a reply from a new agent process", err)
	}
	if got := strings.TrimSpace(resp.Explanation + resp.Command); got != "one two" {
		t.Fatalf("second reply = %q, want %q", got, "one two")
	}
}

// linkState is the transport's link state, for tests.
func (t *ACPTransport) linkState() linkState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.link.state
}
