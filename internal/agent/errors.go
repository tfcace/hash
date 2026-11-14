package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
)

var (
	// ErrACPStartFailed indicates the ACP subprocess could not be started.
	ErrACPStartFailed = errors.New("acp start failed")
	// ErrACPConnectionClosed indicates the ACP stdio connection dropped.
	ErrACPConnectionClosed = errors.New("acp connection closed")
	// ErrACPIdleTimeout indicates no ACP message was received before idle timeout.
	ErrACPIdleTimeout = errors.New("acp idle timeout")
	// ErrACPNoOutput indicates the ACP prompt completed without displayable text.
	ErrACPNoOutput = errors.New("acp prompt completed without output")
	// ErrACPTransportClosed indicates hash closed the transport: final until Restart.
	ErrACPTransportClosed = errors.New("acp transport closed")
	// ErrACPUnsupportedAgent indicates the configured ACP adapter is known incompatible.
	ErrACPUnsupportedAgent = errors.New("unsupported acp agent")
)

// RPCError is the JSON-RPC error an ACP agent replied to a request with:
// the agent's own account of its failure (a usage limit, a lapsed sign-in),
// as opposed to an error hash or the connection produced.
type RPCError struct {
	Code    int
	Message string
	// ErrorKind is the failure's category from the error's data, where the
	// agent gives one (claude-agent-acp's data.errorKind: rate_limit,
	// authentication_failed, ...); "" otherwise.
	ErrorKind string
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// IsStartupError reports whether the error indicates a startup/configuration issue.
func IsStartupError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrACPStartFailed) ||
		errors.Is(err, ErrACPUnsupportedAgent) ||
		errors.Is(err, exec.ErrNotFound)
}

// IsModelSelectionError reports whether the agent rejected the session's
// current model, as Claude Code does for a model the account cannot use. The
// fix is a different model, which the shell's model builtin can select.
func IsModelSelectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "selected model") {
		return true
	}
	return strings.Contains(msg, "model") &&
		(strings.Contains(msg, "not exist") || strings.Contains(msg, "not have access"))
}

// IsTimeoutError reports whether the error indicates a request timeout.
func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrACPIdleTimeout)
}

// IsRetryableError reports whether retrying the request may succeed.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrACPTransportClosed) || IsStartupError(err) {
		return false
	}
	if IsTimeoutError(err) {
		return true
	}
	if errors.Is(err, ErrACPNoOutput) ||
		errors.Is(err, ErrACPConnectionClosed) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	// Fallback for transport errors that may not wrap well across platforms.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection closed")
}
