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
