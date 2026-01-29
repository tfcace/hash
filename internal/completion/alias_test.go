package completion

import (
	"context"
	"testing"
)

// mockFunctionProvider implements the interface for testing
type mockFunctionProvider struct {
	functions []string
}

func (m *mockFunctionProvider) Functions() []string {
	return m.functions
}

func TestAliasCompleter_Name(t *testing.T) {
	c := NewAliasCompleter(&mockFunctionProvider{})
	if c.Name() != "alias" {
		t.Errorf("expected name 'alias', got %s", c.Name())
	}
}

func TestAliasCompleter_MatchesPrefix(t *testing.T) {
	provider := &mockFunctionProvider{
		functions: []string{"myalias", "myfunc", "other"},
	}
	c := NewAliasCompleter(provider)
	ctx := context.Background()

	result, err := c.Complete(ctx, "my", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("expected 2 completions, got %d", len(result.Items))
	}

	// Verify both my* functions are present
	names := make(map[string]bool)
	for _, item := range result.Items {
		names[item.Value] = true
	}
	if !names["myalias"] || !names["myfunc"] {
		t.Errorf("expected myalias and myfunc in results, got %v", names)
	}
}

func TestAliasCompleter_EmptyPrefix(t *testing.T) {
	provider := &mockFunctionProvider{
		functions: []string{"foo", "bar", "baz"},
	}
	c := NewAliasCompleter(provider)
	ctx := context.Background()

	result, err := c.Complete(ctx, "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 3 {
		t.Errorf("expected 3 completions, got %d", len(result.Items))
	}
}

func TestAliasCompleter_NoMatches(t *testing.T) {
	provider := &mockFunctionProvider{
		functions: []string{"foo", "bar"},
	}
	c := NewAliasCompleter(provider)
	ctx := context.Background()

	result, err := c.Complete(ctx, "xyz", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("expected 0 completions, got %d", len(result.Items))
	}
}

func TestAliasCompleter_CaseSensitive(t *testing.T) {
	provider := &mockFunctionProvider{
		functions: []string{"MyFunc", "myfunc"},
	}
	c := NewAliasCompleter(provider)
	ctx := context.Background()

	// Only lowercase should match
	result, err := c.Complete(ctx, "my", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("expected 1 completion (case-sensitive), got %d", len(result.Items))
	}
	if result.Items[0].Value != "myfunc" {
		t.Errorf("expected 'myfunc', got %s", result.Items[0].Value)
	}
}
