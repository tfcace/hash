package readline

import (
	"context"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/completion"
)

func TestReadlineCompleter_Adapts(t *testing.T) {
	// Create a mock router with file completer
	router := completion.NewRouter()
	router.Register(completion.NewFileCompleter(), completion.PriorityFilesystem)

	adapter := NewCompleterAdapter(router)
	if adapter == nil {
		t.Fatal("NewCompleterAdapter() returned nil")
	}
}

// mockCompleter returns predictable completions for testing
type mockCompleter struct {
	items []completion.Item
}

func (m *mockCompleter) Name() string { return "mock" }
func (m *mockCompleter) Complete(ctx context.Context, line string, pos int) (completion.Result, error) {
	return completion.Result{Items: m.items, Prefix: ""}, nil
}

type slowCompleter struct {
	delay time.Duration
}

func (m slowCompleter) Name() string { return "slow" }
func (m slowCompleter) Complete(ctx context.Context, line string, pos int) (completion.Result, error) {
	select {
	case <-time.After(m.delay):
		return completion.Result{Items: []completion.Item{{Value: "late-result"}}}, nil
	case <-ctx.Done():
		return completion.Result{}, nil
	}
}

func TestCompleterAdapter_CutsOffSlowCompleter(t *testing.T) {
	router := completion.NewRouter()
	router.Register(slowCompleter{delay: 300 * time.Millisecond}, completion.PriorityFilesystem)
	adapter := NewCompleterAdapter(router)

	start := time.Now()
	candidates, length := adapter.Do([]rune("slow "), len("slow "))
	elapsed := time.Since(start)

	if elapsed > 250*time.Millisecond {
		t.Fatalf("CompleterAdapter.Do() took %s, want under 250ms", elapsed)
	}
	if len(candidates) != 0 || length != 0 {
		t.Fatalf("slow completion should be cut off, got candidates=%q length=%d", candidates, length)
	}
}

func TestCompleterAdapter_ReturnsSuffix(t *testing.T) {
	// This test verifies the fix for the bug where typing "inter" and completing
	// to "internal/" would result in "interinternal/" instead of "internal/"
	// See: https://pkg.go.dev/github.com/chzyer/readline - Do() should return suffixes

	mock := &mockCompleter{
		items: []completion.Item{{Value: "internal/", Display: "internal"}},
	}
	router := completion.NewRouter()
	router.Register(mock, completion.PriorityFilesystem)
	adapter := NewCompleterAdapter(router)

	// Simulate typing "cd inter" with cursor at end (pos=8)
	line := []rune("cd inter")
	pos := 8

	candidates, length := adapter.Do(line, pos)

	// Should return suffix "nal/" (not full "internal/") with length 5 (matching "inter")
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if string(candidates[0]) != "nal/" {
		t.Errorf("expected suffix 'nal/', got '%s'", string(candidates[0]))
	}
	if length != 5 {
		t.Errorf("expected length 5, got %d", length)
	}
}

func TestCompleterAdapter_CaseInsensitive(t *testing.T) {
	// Test that case-insensitive matching works correctly
	mock := &mockCompleter{
		items: []completion.Item{{Value: "Internal/", Display: "Internal"}},
	}
	router := completion.NewRouter()
	router.Register(mock, completion.PriorityFilesystem)
	adapter := NewCompleterAdapter(router)

	// User types lowercase "inter", matches "Internal/"
	line := []rune("cd inter")
	pos := 8

	candidates, _ := adapter.Do(line, pos)

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	// Should strip the 5-char prefix case-insensitively
	if string(candidates[0]) != "nal/" {
		t.Errorf("expected suffix 'nal/', got '%s'", string(candidates[0]))
	}
}
