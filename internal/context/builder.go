package context

import (
	"os"
	"os/exec"
	"strings"
)

// Builder builds a context collection from various sources.
type Builder struct {
	collection *Collection
}

// NewBuilder creates a new context builder.
func NewBuilder() *Builder {
	return &Builder{
		collection: NewCollection(),
	}
}

// AutoDetect adds auto-detected context items (cwd, git branch, k8s context).
func (b *Builder) AutoDetect() *Builder {
	// Current working directory
	if cwd, err := os.Getwd(); err == nil {
		b.collection.Add(Item{
			Category: CategoryAutoDetect,
			Key:      "cwd",
			Value:    cwd,
			Selected: true, // Auto-detected items are selected by default
		})
	}

	// Git branch
	if branch := detectGitBranch(); branch != "" {
		b.collection.Add(Item{
			Category: CategoryAutoDetect,
			Key:      "git branch",
			Value:    branch,
			Selected: true,
		})
	}

	// Kubernetes context
	if kubeCtx := detectKubeContext(); kubeCtx != "" {
		b.collection.Add(Item{
			Category: CategoryAutoDetect,
			Key:      "kube context",
			Value:    kubeCtx,
			Selected: true,
		})
	}

	return b
}

// WithHistory adds command history items.
func (b *Builder) WithHistory(commands []string) *Builder {
	for _, cmd := range commands {
		b.collection.Add(Item{
			Category: CategoryHistory,
			Key:      truncateString(cmd, 40),
			Value:    cmd,
			Selected: false, // History items are not selected by default
		})
	}
	return b
}

// WithHistoryLimit adds command history items with a limit.
func (b *Builder) WithHistoryLimit(commands []string, limit int) *Builder {
	start := 0
	if len(commands) > limit {
		start = len(commands) - limit
	}
	return b.WithHistory(commands[start:])
}

// WithEnvVars adds environment variables.
func (b *Builder) WithEnvVars(names []string) *Builder {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			b.collection.Add(Item{
				Category: CategoryEnv,
				Key:      name,
				Value:    value,
				Selected: false, // Env vars are not selected by default
			})
		}
	}
	return b
}

// WithCustom adds a custom context item.
func (b *Builder) WithCustom(key, value string) *Builder {
	b.collection.Add(Item{
		Category: CategoryCustom,
		Key:      key,
		Value:    value,
		Selected: true,
	})
	return b
}

// WithLastOutput adds the last command output.
func (b *Builder) WithLastOutput(output string) *Builder {
	if output != "" {
		b.collection.Add(Item{
			Category: CategoryAutoDetect,
			Key:      "last output",
			Value:    truncateString(output, 1000),
			Selected: false,
		})
	}
	return b
}

// WithLastError adds the last command error.
func (b *Builder) WithLastError(stderr string) *Builder {
	if stderr != "" {
		b.collection.Add(Item{
			Category: CategoryAutoDetect,
			Key:      "last error",
			Value:    truncateString(stderr, 500),
			Selected: true, // Errors are selected by default (useful for ?? explain)
		})
	}
	return b
}

// Build returns the built collection.
func (b *Builder) Build() *Collection {
	return b.collection
}

// detectGitBranch returns the current git branch, or empty string if not in a git repo.
func detectGitBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// detectKubeContext returns the current kubectl context, or empty string.
func detectKubeContext() string {
	cmd := exec.Command("kubectl", "config", "current-context")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// truncateString truncates a string to max length with ellipsis.
func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
