package completion

import (
	"context"
	"testing"
)

func TestEnvCompleter_Name(t *testing.T) {
	c := NewEnvCompleter(nil)
	if c.Name() != "env" {
		t.Errorf("expected name 'env', got %s", c.Name())
	}
}

func TestEnvCompleter_MatchesDollarPrefix(t *testing.T) {
	c := NewEnvCompleter(nil)
	c.envFunc = func() []string {
		return []string{"HOME=/home/user", "PATH=/usr/bin", "HISTFILE=/home/user/.history"}
	}
	ctx := context.Background()

	result, err := c.Complete(ctx, "echo $HI", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("expected 1 completion, got %d: %v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "$HISTFILE" {
		t.Errorf("expected $HISTFILE, got %s", result.Items[0].Value)
	}
}

func TestEnvCompleter_MatchesAllOnDollar(t *testing.T) {
	c := NewEnvCompleter(nil)
	c.envFunc = func() []string {
		return []string{"HOME=/home/user", "PATH=/usr/bin"}
	}
	ctx := context.Background()

	result, err := c.Complete(ctx, "echo $", 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("expected 2 completions, got %d", len(result.Items))
	}
}

func TestEnvCompleter_NoDollar_NoMatch(t *testing.T) {
	c := NewEnvCompleter(nil)
	c.envFunc = func() []string {
		return []string{"HOME=/home/user", "PATH=/usr/bin"}
	}
	ctx := context.Background()

	result, err := c.Complete(ctx, "echo HO", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("expected 0 completions (no $), got %d", len(result.Items))
	}
}

func TestEnvCompleter_BraceSyntax(t *testing.T) {
	c := NewEnvCompleter(nil)
	c.envFunc = func() []string {
		return []string{"HOME=/home/user", "HOSTNAME=localhost"}
	}
	ctx := context.Background()

	result, err := c.Complete(ctx, "echo ${HO", 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("expected 2 completions, got %d", len(result.Items))
	}
}

func TestEnvCompleter_CaseSensitive(t *testing.T) {
	c := NewEnvCompleter(nil)
	c.envFunc = func() []string {
		return []string{"HOME=/home/user", "home=/tmp"}
	}
	ctx := context.Background()

	result, err := c.Complete(ctx, "echo $HO", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("expected 1 completion (case-sensitive), got %d", len(result.Items))
	}
	if result.Items[0].Value != "$HOME" {
		t.Errorf("expected $HOME, got %s", result.Items[0].Value)
	}
}

func TestEnvCompleter_MidLine(t *testing.T) {
	c := NewEnvCompleter(nil)
	c.envFunc = func() []string {
		return []string{"HOME=/home/user", "PATH=/usr/bin"}
	}
	ctx := context.Background()

	// Cursor at position 8 (after $HO), with more text after
	result, err := c.Complete(ctx, "echo $HO something", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("expected 1 completion, got %d", len(result.Items))
	}
	if result.Items[0].Value != "$HOME" {
		t.Errorf("expected $HOME, got %s", result.Items[0].Value)
	}
}

func TestEnvCompleter_ShowsValue(t *testing.T) {
	c := NewEnvCompleter(nil)
	c.envFunc = func() []string {
		return []string{"HOME=/home/user"}
	}
	ctx := context.Background()

	result, err := c.Complete(ctx, "echo $HO", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(result.Items))
	}
	// Description should show the value (possibly truncated)
	if result.Items[0].Description != "/home/user" {
		t.Errorf("expected description '/home/user', got %s", result.Items[0].Description)
	}
}

func TestEnvCompleter_TruncatesLongValue(t *testing.T) {
	c := NewEnvCompleter(nil)
	c.SetMaskSensitive(false) // Disable masking for this test
	longValue := "/very/long/path/that/exceeds/the/maximum/display/length/for/the/completion/menu"
	c.envFunc = func() []string {
		return []string{"LONGVAR=" + longValue}
	}
	ctx := context.Background()

	result, err := c.Complete(ctx, "echo $LONG", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(result.Items))
	}
	// Description should be truncated with ellipsis
	if len(result.Items[0].Description) > 43 {
		t.Errorf("expected truncated description, got length %d", len(result.Items[0].Description))
	}
}

func TestIsSensitiveEnvName(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"API_KEY", true},
		{"OPENAI_API_KEY", true},
		{"SECRET_TOKEN", true},
		{"AWS_SECRET_ACCESS_KEY", true},
		{"PASSWORD", true},
		{"DB_PASSWORD", true},
		{"GITHUB_TOKEN", true},
		{"AUTH_TOKEN", true},
		{"PRIVATE_KEY", true},
		{"CREDENTIAL", true},
		{"HOME", false},
		{"PATH", false},
		{"USER", false},
		{"SHELL", false},
		{"EDITOR", false},
		{"LANG", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSensitiveEnvName(tt.name)
			if got != tt.expected {
				t.Errorf("isSensitiveEnvName(%q) = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

func TestMaskValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sk-proj-abc123xyz", "sk-p••••••••"}, // Long key: 4 visible + 8 bullets
		{"secret", "secr••"},                  // 6 chars: 4 visible + 2 bullets
		{"abc", "•••"},                        // Short: all masked
		{"ab", "••"},                          // Very short: all masked
		{"", ""},                              // Empty
		{"abcd", "••••"},                      // Exactly 4: all masked (nothing to show after)
		{"abcde", "abcd•"},                    // 5 chars: 4 visible + 1 bullet
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := maskValue(tt.input)
			if got != tt.expected {
				t.Errorf("maskValue(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestEnvCompleter_MasksSensitiveValues(t *testing.T) {
	c := NewEnvCompleter(nil)
	// Masking is enabled by default
	c.envFunc = func() []string {
		return []string{"API_KEY=sk-proj-abc123xyz789", "HOME=/home/user"}
	}
	ctx := context.Background()

	result, err := c.Complete(ctx, "echo $", 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 2 {
		t.Fatalf("expected 2 completions, got %d", len(result.Items))
	}

	// Find API_KEY and HOME in results
	var apiKeyDesc, homeDesc string
	for _, item := range result.Items {
		if item.Display == "API_KEY" {
			apiKeyDesc = item.Description
		} else if item.Display == "HOME" {
			homeDesc = item.Description
		}
	}

	// API_KEY should be masked
	if apiKeyDesc != "sk-p••••••••" {
		t.Errorf("API_KEY description should be masked, got %q", apiKeyDesc)
	}

	// HOME should not be masked
	if homeDesc != "/home/user" {
		t.Errorf("HOME description should not be masked, got %q", homeDesc)
	}
}

func TestEnvCompleter_MaskingDisabled(t *testing.T) {
	c := NewEnvCompleter(nil)
	c.SetMaskSensitive(false) // Disable masking
	c.envFunc = func() []string {
		return []string{"API_KEY=sk-proj-abc123xyz789"}
	}
	ctx := context.Background()

	result, err := c.Complete(ctx, "echo $API", 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(result.Items))
	}

	// With masking disabled, full value should be shown (truncated if needed)
	if result.Items[0].Description != "sk-proj-abc123xyz789" {
		t.Errorf("expected unmasked value, got %q", result.Items[0].Description)
	}
}
