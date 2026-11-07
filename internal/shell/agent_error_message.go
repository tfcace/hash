package shell

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/trace"
)

// agentStreamErrorMessage is how a failed request reads in the turn's
// failed row and status.
func agentStreamErrorMessage(err error) string {
	if errors.Is(err, agent.ErrACPNoOutput) {
		return emptyAgentResponseMessage
	}
	return readableAgentErr(err).Error()
}

// readableAgentError is a well-known agent failure said in one readable
// line. The agent's own error stays behind it, for errors.Is and the trace.
type readableAgentError struct {
	msg string
	err error
}

func (e *readableAgentError) Error() string { return e.msg }
func (e *readableAgentError) Unwrap() error { return e.err }

// readableAgentErr says err in one readable line when the agent's own
// error (agent.RPCError) is a failure the user acts on outside hash, a usage
// or rate limit or a sign-in that lapsed, keeping the agent's words and
// dropping the JSON-RPC and API wrapping around them. Any other error,
// hash's own and the connection's included, is returned as it is.
func readableAgentErr(err error) error {
	var readable *readableAgentError
	var rpc *agent.RPCError
	if err == nil || errors.As(err, &readable) || !errors.As(err, &rpc) {
		return err
	}
	failure, detail := agentFailureOf(rpc)
	var lead string
	switch failure {
	case agentUsageLimit:
		lead = "Usage limit reached: "
	case agentSignedOut:
		lead = "Not signed in to the agent: "
	default:
		return err
	}
	msg := lead + detail
	trace.AgentHigh("agent_error", map[string]any{"error": err.Error(), "shown": msg})
	return &readableAgentError{msg: msg, err: err}
}

// agentFailure is what an agent's error says went wrong, as far as hash
// says it readably.
type agentFailure int

const (
	agentFailureOther agentFailure = iota
	agentUsageLimit                // a usage or rate limit
	agentSignedOut                 // the agent is not signed in, or its sign-in lapsed
)

// acpAuthRequired is the JSON-RPC code ACP gives an agent that needs the
// user to sign in.
const acpAuthRequired = -32000

var (
	rpcInternalPrefix = regexp.MustCompile(`^Internal error: `)
	apiError          = regexp.MustCompile(`(?s)^API Error: (\d{3}) (.*)$`)
	resetEpoch        = regexp.MustCompile(`\|(\d{9,11})$`)
)

// agentFailureOf reads rpc for what failed and the agent's own sentence
// about it. The kind the agent names decides alone; then an API error's
// status and type; then ACP's sign-in code; only an error with none of
// them is read for well-known words, and one about the model never counts
// (the model hint answers it).
func agentFailureOf(rpc *agent.RPCError) (failure agentFailure, detail string) {
	detail = strings.TrimSpace(rpcInternalPrefix.ReplaceAllString(rpc.Message, ""))
	if rpc.ErrorKind != "" {
		failure = agentFailureOfKind(rpc.ErrorKind)
	} else if m := apiError.FindStringSubmatch(detail); m != nil {
		var body struct {
			Error struct{ Type, Message string }
		}
		if json.Unmarshal([]byte(m[2]), &body) == nil && body.Error.Message != "" {
			detail = body.Error.Message
		} else {
			detail = m[2]
		}
		switch {
		case m[1] == "429" || body.Error.Type == "rate_limit_error":
			failure = agentUsageLimit
		case m[1] == "401" || body.Error.Type == "authentication_error":
			failure = agentSignedOut
		}
	} else {
		failure = agentFailureInWords(rpc, detail)
	}
	if m := resetEpoch.FindStringSubmatchIndex(detail); m != nil {
		if secs, err := strconv.ParseInt(detail[m[2]:m[3]], 10, 64); err == nil {
			detail = detail[:m[0]] + " (resets " + time.Unix(secs, 0).Format("15:04") + ")"
		}
	}
	if i := strings.IndexByte(detail, '\n'); i >= 0 {
		detail = detail[:i]
	}
	return failure, truncateTerminalText(sanitizeTerminalText(strings.TrimSpace(detail)), 120)
}

// agentFailureOfKind is what the kind an agent names its failure with
// (claude-agent-acp's data.errorKind) says went wrong.
func agentFailureOfKind(kind string) agentFailure {
	switch kind {
	case "rate_limit", "billing_error":
		return agentUsageLimit
	case "authentication_failed":
		return agentSignedOut
	}
	return agentFailureOther
}

// agentFailureInWords is what an agent error with no kind and no API status
// says went wrong: ACP's authRequired code (-32000, which JSON-RPC leaves
// to each implementation and ACP gives sign-in), else the phrases agents
// use for a limit or a lapsed sign-in, never a lone word that a path or a
// server's name could hold.
func agentFailureInWords(rpc *agent.RPCError, detail string) agentFailure {
	if rpc.Code == acpAuthRequired {
		return agentSignedOut
	}
	if agent.IsModelSelectionError(rpc) {
		return agentFailureOther
	}
	lower := strings.ToLower(detail)
	switch {
	case containsAny(lower, "usage limit", "rate limit", "too many requests", "credit balance", "resource_exhausted", "quota exceeded", "exceeded your current quota") ||
		strings.Contains(lower, "hit your") && strings.Contains(lower, "limit"):
		return agentUsageLimit
	case containsAny(lower, "authentication required", "authentication failed", "invalid api key", "not logged in", "please run /login", "oauth token"):
		return agentSignedOut
	}
	return agentFailureOther
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
