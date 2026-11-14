package agent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ContextBuilder builds agent context.
type ContextBuilder struct {
	ctx Context
}

// NewContextBuilder creates a new context builder with defaults.
func NewContextBuilder() *ContextBuilder {
	cwd, _ := os.Getwd()
	return &ContextBuilder{
		ctx: Context{
			Cwd:     cwd,
			EnvVars: make(map[string]string),
		},
	}
}

// WithHistory adds command history to the context.
func (b *ContextBuilder) WithHistory(history []string) *ContextBuilder {
	b.ctx.History = history
	return b
}

// WithEnvVars adds specified environment variables to the context.
func (b *ContextBuilder) WithEnvVars(vars []string) *ContextBuilder {
	for _, name := range vars {
		if val := os.Getenv(name); val != "" {
			b.ctx.EnvVars[name] = val
		}
	}
	return b
}

// WithLastOutput adds the last command output to the context.
func (b *ContextBuilder) WithLastOutput(output string) *ContextBuilder {
	b.ctx.LastOutput = output
	return b
}

// WithLastError adds the last error to the context.
func (b *ContextBuilder) WithLastError(err string) *ContextBuilder {
	b.ctx.LastError = err
	return b
}

// DetectGitBranch detects the current git branch.
func (b *ContextBuilder) DetectGitBranch(ctx context.Context) *ContextBuilder {
	b.ctx.GitBranch = detectContextValue(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	return b
}

// DetectKubeContext detects the current kubectl context.
func (b *ContextBuilder) DetectKubeContext(ctx context.Context) *ContextBuilder {
	b.ctx.KubeContext = detectContextValue(ctx, "kubectl", "config", "current-context")
	return b
}

// Context is optional: a slow tool (including a kubectl launcher fetching its
// binary) must not hold up the agent request or prevent its cancellation.
const contextProbeTimeout = 250 * time.Millisecond

func detectContextValue(parent context.Context, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(parent, contextProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 50 * time.Millisecond
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	out, err := cmd.Output()
	if err != nil {
		// A launcher can exit while a descendant keeps its output open.
		// WaitDelay bounds the pipe read; end that descendant too.
		if cmd.Process != nil {
			_ = cmd.Cancel()
		}
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Build returns the built context.
func (b *ContextBuilder) Build() Context {
	return b.ctx
}
