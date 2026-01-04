package completion

import (
	"context"
	"testing"
)

// MockCompleter for testing
type MockCompleter struct {
	name   string
	items  []Item
	called bool
}

func (m *MockCompleter) Name() string { return m.name }
func (m *MockCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	m.called = true
	return Result{Items: m.items}, nil
}

func TestRouter_FirstCompleterWins(t *testing.T) {
	mock1 := &MockCompleter{
		name:  "first",
		items: []Item{{Value: "from-first"}},
	}
	mock2 := &MockCompleter{
		name:  "second",
		items: []Item{{Value: "from-second"}},
	}

	router := NewRouter()
	router.Register(mock1, 100) // Higher priority (lower number)
	router.Register(mock2, 200) // Lower priority

	ctx := context.Background()
	result, err := router.Complete(ctx, "test ", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// First completer should win (has results)
	if !mock1.called {
		t.Error("First completer was not called")
	}
	if len(result.Items) != 1 {
		t.Errorf("Items count = %d, want 1", len(result.Items))
	}
	if result.Items[0].Value != "from-first" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "from-first")
	}
}

func TestRouter_FallsThrough(t *testing.T) {
	mock1 := &MockCompleter{
		name:  "first",
		items: []Item{}, // No results
	}
	mock2 := &MockCompleter{
		name:  "second",
		items: []Item{{Value: "from-second"}},
	}

	router := NewRouter()
	router.Register(mock1, 100) // Higher priority (lower number)
	router.Register(mock2, 200) // Lower priority

	ctx := context.Background()
	result, err := router.Complete(ctx, "test ", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should fall through to second
	if !mock1.called {
		t.Error("First completer was not called")
	}
	if !mock2.called {
		t.Error("Second completer was not called")
	}
	if len(result.Items) != 1 {
		t.Errorf("Items count = %d, want 1", len(result.Items))
	}
	if result.Items[0].Value != "from-second" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "from-second")
	}
}

func TestRouter_PriorityOrdering(t *testing.T) {
	router := NewRouter()

	// Register in wrong order
	mock1 := &MockCompleter{name: "low", items: []Item{{Value: "low"}}}
	mock2 := &MockCompleter{name: "high", items: []Item{{Value: "high"}}}

	router.Register(mock1, 300) // Lower priority (higher number)
	router.Register(mock2, 100) // Higher priority (lower number)

	ctx := context.Background()
	result, _ := router.Complete(ctx, "test ", 5)

	// High priority should be called first and win
	if result.Items[0].Value != "high" {
		t.Errorf("Value = %q, want %q (high priority)", result.Items[0].Value, "high")
	}
}

func TestRouter_FuzzyFiltering(t *testing.T) {
	router := NewRouter()
	router.SetFuzzy(true)

	// Create a mock completer that returns fixed items
	mock := &MockCompleter{
		name: "mock",
		items: []Item{
			{Value: "config.toml"},
			{Value: "context.go"},
			{Value: "container.yaml"},
			{Value: "readme.md"},
		},
	}
	router.Register(mock, PriorityFilesystem)

	// Complete with query "cont" - should fuzzy match context and container
	result, err := router.Complete(context.Background(), "cat cont", 8)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should get fuzzy matches sorted by score
	if len(result.Items) < 2 {
		t.Errorf("Expected at least 2 fuzzy matches, got %d", len(result.Items))
	}

	// First result should be best match (container or context)
	if result.Items[0].Value != "container.yaml" && result.Items[0].Value != "context.go" {
		t.Errorf("First result should be container or context, got %q", result.Items[0].Value)
	}
}

func TestRouter_FuzzyDisabled(t *testing.T) {
	router := NewRouter()
	router.SetFuzzy(false)

	mock := &MockCompleter{
		name: "mock",
		items: []Item{
			{Value: "config.toml"},
			{Value: "readme.md"},
		},
	}
	router.Register(mock, PriorityFilesystem)

	// With fuzzy disabled, should return items as-is
	result, err := router.Complete(context.Background(), "cat ", 4)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("Expected 2 items unchanged, got %d", len(result.Items))
	}
}
