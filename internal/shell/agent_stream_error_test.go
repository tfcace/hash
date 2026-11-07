package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
)

func TestHandleAgentStreamError_NoResponseStartupShowsTroubleshooting(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		config:      &config.Config{},
		responseUI:  NewResponseUI(&out),
		agentOutput: NewAgentOutputCoordinator(&out),
	}
	sh.config.Agent.Transport = "stdio"
	sh.config.Agent.Command = "claude-agent-acp"

	retry := sh.handleAgentStreamError(
		startTurn(t, sh),
		errors.Join(agent.ErrACPStartFailed, exec.ErrNotFound),
		0,
	)

	if retry {
		t.Fatal("handleAgentStreamError() asked to retry with no key pressed")
	}

	output := out.String()
	if !strings.Contains(output, "Troubleshooting") {
		t.Fatalf("expected troubleshooting output, got:\n%s", output)
	}
	if !strings.Contains(output, "installed") {
		t.Fatalf("expected install hint in output, got:\n%s", output)
	}
}

func TestHandleAgentStreamError_NoResponseTimeoutDoesNotShowTroubleshooting(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		config:      &config.Config{},
		responseUI:  NewResponseUI(&out),
		agentOutput: NewAgentOutputCoordinator(&out),
	}

	retry := sh.handleAgentStreamError(
		startTurn(t, sh),
		errors.Join(agent.ErrACPIdleTimeout, context.DeadlineExceeded),
		0,
	)

	if retry {
		t.Fatal("handleAgentStreamError() asked to retry with no key pressed")
	}

	output := out.String()
	if strings.Contains(output, "Troubleshooting") {
		t.Fatalf("did not expect troubleshooting hints for timeout, got:\n%s", output)
	}
	if !strings.Contains(output, "enter to retry") {
		t.Fatalf("expected retry hint for timeout, got:\n%s", output)
	}
}

func TestHandleAgentStreamError_NoOutputUsesUserFacingRetry(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		config:      &config.Config{},
		responseUI:  NewResponseUI(&out),
		agentOutput: NewAgentOutputCoordinator(&out),
	}

	retry := sh.handleAgentStreamError(
		startTurn(t, sh),
		errors.Join(agent.ErrACPNoOutput, errors.New("prompt completed without displayable text (stopReason=end_turn)")),
		0,
	)

	if retry {
		t.Fatal("handleAgentStreamError() asked to retry with no key pressed")
	}

	output := out.String()
	if !strings.Contains(output, "agent ended the turn without text") {
		t.Fatalf("expected user-facing no-output message, got:\n%s", output)
	}
	if strings.Contains(output, "acp prompt completed") || strings.Contains(output, "stopReason=end_turn") {
		t.Fatalf("expected protocol details to stay out of the UI, got:\n%s", output)
	}
	if !strings.Contains(output, "enter to retry") {
		t.Fatalf("expected retry hint for no-output error, got:\n%s", output)
	}
}

func TestHandleAgentStreamError_ModelErrorPointsAtModelBuiltin(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		config:      &config.Config{},
		responseUI:  NewResponseUI(&out),
		agentOutput: NewAgentOutputCoordinator(&out),
	}

	retry := sh.handleAgentStreamError(
		startTurn(t, sh),
		errors.New("rpc error -32603: Internal error: There's an issue with the selected model (fable[1m]). It may not exist or you may not have access to it. Run --model to pick a different model."),
		0,
	)

	if retry {
		t.Fatal("handleAgentStreamError() asked to retry with no key pressed")
	}
	output := out.String()
	if !strings.Contains(output, "model --list") {
		t.Fatalf("expected a pointer to the model builtin, got:\n%s", output)
	}
	if strings.Contains(output, "Troubleshooting") || strings.Contains(output, "enter to retry") {
		t.Fatalf("model errors should get neither install hints nor a retry prompt, got:\n%s", output)
	}
}

// Well-known agent failures (a usage or rate limit, a sign-in that lapsed)
// reach the user as one readable line in the agent's own words, not the
// JSON-RPC wrapping around them; anything else is shown as it came.
func TestAgentStreamErrorMessage_SaysWellKnownFailuresReadably(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want string
	}{
		{rpcErr(-32603, "Internal error: You've hit your 5-hour limit"), "Usage limit reached: You've hit your 5-hour limit"},
		{rpcErr(-32603, "Internal error: Claude AI usage limit reached|1762534800"), "Usage limit reached: Claude AI usage limit reached (resets " + time.Unix(1762534800, 0).Format("15:04") + ")"},
		{rpcErr(-32603, `Internal error: API Error: 429 {"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your rate limit"}}`), "Usage limit reached: Number of requests has exceeded your rate limit"},
		{rpcErr(-32000, "Authentication required"), "Not signed in to the agent: Authentication required"},
		{rpcErr(-32603, "Internal error: Invalid API key · Please run /login"), "Not signed in to the agent: Invalid API key · Please run /login"},
		{rpcErr(-32603, `Internal error: API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired."}}`), "Not signed in to the agent: OAuth token has expired."},
		{fmt.Errorf("initialize: %w", rpcErr(-32000, "Authentication required")), "Not signed in to the agent: Authentication required"},
		{rpcErr(-32603, "Internal error: boom"), "rpc error -32603: Internal error: boom"},
		{errors.New("connection reset by peer"), "connection reset by peer"},
	} {
		if got := agentStreamErrorMessage(tt.err); got != tt.want {
			t.Errorf("agentStreamErrorMessage(%q)\n = %q\nwant %q", tt.err, got, tt.want)
		}
	}
}

// Only the agent's own error is read for a limit or a sign-in, and only for
// what it says the failure is: a word that happens to appear in a path, a
// command hash denied, stderr joined to a lost connection, or an error
// about the model is shown as it came.
func TestAgentStreamErrorMessage_ReadsOnlyTheAgentsOwnFailure(t *testing.T) {
	for _, err := range []error{
		inlineToolDenied("gcloud alpha services quota list"),
		inlineToolDenied("npm login # /login"),
		inlineToolDenied("gh auth status # authentication"),
		errors.Join(agent.ErrACPConnectionClosed, errors.New("adapter stderr: 429 Too Many Requests")),
		errors.New("rpc error -32603: Internal error: You've hit your 5-hour limit"), // text alone is not the agent's reply
		rpcErr(-32603, "Internal error: ENOENT: no such file or directory, open '/Users/a/quota.toml'"),
		rpcErr(-32603, `Internal error: Request failed: the server said "unauthorized origin" for the MCP server git-mcp`),
		rpcErr(-32603, "Internal error: There's an issue with the selected model (x). It may not exist or you may not have access to it. Run /login or --model to pick a different model."),
		rpcErr(-32603, `Internal error: API Error: 500 {"type":"error","error":{"type":"api_error","message":"the rate limiter is down"}}`),
	} {
		if got := agentStreamErrorMessage(err); got != err.Error() {
			t.Errorf("agentStreamErrorMessage(%q)\n = %q, want it as it came", err, got)
		}
	}
}

func rpcErr(code int, msg string) error { return &agent.RPCError{Code: code, Message: msg} }

func kindErr(code int, msg, kind string) error {
	return &agent.RPCError{Code: code, Message: msg, ErrorKind: kind}
}

// An agent that names its failure's kind (claude-agent-acp's
// data.errorKind) is taken at its word: the kind decides, not the code or
// the words. -32000 is a code JSON-RPC leaves to each implementation, and
// a limit sent with it read as a lapsed sign-in.
func TestAgentStreamErrorMessage_TheAgentsErrorKindDecides(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want string
	}{
		{kindErr(-32000, "Rate limited, try again later", "rate_limit"), "Usage limit reached: Rate limited, try again later"},
		{kindErr(-32603, "Internal error: Your session expired", "authentication_failed"), "Not signed in to the agent: Your session expired"},
		{kindErr(-32603, "Internal error: Credit balance is too low", "billing_error"), "Usage limit reached: Credit balance is too low"},
		{kindErr(-32603, "Internal error: upstream rate limit service crashed", "server_error"), "rpc error -32603: Internal error: upstream rate limit service crashed"},
		{kindErr(-32000, "Internal error: worker went away", "worker_shutdown"), "rpc error -32000: Internal error: worker went away"},
	} {
		if got := agentStreamErrorMessage(tt.err); got != tt.want {
			t.Errorf("agentStreamErrorMessage(%q, kind %q)\n = %q\nwant %q", tt.err, tt.err.(*agent.RPCError).ErrorKind, got, tt.want)
		}
	}
}

// A full turn's failed row and an inline fill's notice both say a usage
// limit readably.
func TestAgentStreamError_ALimitReadsAsOneLine(t *testing.T) {
	limit := rpcErr(-32603, "Internal error: You've hit your 5-hour limit")

	sh, screen, _ := newTurnShell(t, &scriptedTransport{})
	sh.handleAgentStreamError(startTurn(t, sh), limit, 0)
	if out := screen.String(); strings.Contains(out, "rpc error") || !strings.Contains(out, "Usage limit reached: You've hit your 5-hour limit") {
		t.Errorf("full turn drew %q", out)
	}

	events := make(chan agent.StreamEvent)
	errs := make(chan error, 1)
	errs <- limit
	close(events)
	close(errs)
	var ended error
	_, errCh := textStreamFromEvents(context.Background(), events, errs, func(_ string, err error) { ended = err })
	got := <-errCh
	for range errCh {
	}
	if got == nil || got.Error() != "Usage limit reached: You've hit your 5-hour limit" || ended == nil || ended.Error() != got.Error() {
		t.Errorf("inline fill ended with %v (turn told %v), want the readable line", got, ended)
	}
	if !errors.Is(got, limit) {
		t.Error("the readable error lost the agent's error behind it")
	}
}

// A builtin that asks the agent itself (completions generate) gets the
// same readable line for a usage limit as a ?? turn.
func TestAskText_ALimitReadsAsOneLine(t *testing.T) {
	limit := rpcErr(-32603, "Internal error: You've hit your 5-hour limit")
	h := NewAgentHandler(agent.NewClient(failingTransport{err: limit}))
	_, err := h.AskText(context.Background(), "generate a spec")
	if err == nil || err.Error() != "Usage limit reached: You've hit your 5-hour limit" || !errors.Is(err, limit) {
		t.Errorf("AskText() error = %v, want the readable line wrapping the agent's error", err)
	}
}

// failingTransport fails every request with err.
type failingTransport struct {
	namedTestTransport
	err error
}

func (f failingTransport) SendStreaming(context.Context, agent.Request) (<-chan string, <-chan error) {
	text, errs := make(chan string), make(chan error, 1)
	errs <- f.err
	close(text)
	close(errs)
	return text, errs
}
