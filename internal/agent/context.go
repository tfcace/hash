package agent

import (
	"os"
	"os/exec"
	"strings"
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
func (b *ContextBuilder) DetectGitBranch() *ContextBuilder {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err == nil {
		b.ctx.GitBranch = strings.TrimSpace(string(out))
	}
	return b
}

// DetectKubeContext detects the current kubectl context.
func (b *ContextBuilder) DetectKubeContext() *ContextBuilder {
	cmd := exec.Command("kubectl", "config", "current-context")
	out, err := cmd.Output()
	if err == nil {
		b.ctx.KubeContext = strings.TrimSpace(string(out))
	}
	return b
}

// Build returns the built context.
func (b *ContextBuilder) Build() Context {
	return b.ctx
}
