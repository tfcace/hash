package agent

import (
	"context"
	"errors"
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
	// ErrACPUnsupportedAgent indicates the configured ACP adapter is known incompatible.
	ErrACPUnsupportedAgent = errors.New("unsupported acp agent")
)

// IsStartupError reports whether the error indicates a startup/configuration issue.
func IsStartupError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrACPStartFailed) ||
		errors.Is(err, ErrACPUnsupportedAgent) ||
		errors.Is(err, exec.ErrNotFound)
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
	if errors.Is(err, context.Canceled) || IsStartupError(err) {
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
