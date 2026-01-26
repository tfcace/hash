package completion

import (
	"context"
	"testing"
)

func TestExecutableCompleter_Name(t *testing.T) {
	c := NewExecutableCompleter()
	if c.Name() != "executable" {
		t.Errorf("expected name 'executable', got %s", c.Name())
	}
}

func TestExecutableCompleter_CommandPosition(t *testing.T) {
	c := NewExecutableCompleter()
	ctx := context.Background()

	// Should complete at the start of a line
	result, err := c.Complete(ctx, "ls", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have some results (ls should match several commands)
	if len(result.Items) == 0 {
		t.Error("expected some completions for 'ls'")
	}

	// Verify ls is in the results
	found := false
	for _, item := range result.Items {
		if item.Value == "ls" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'ls' in completions")
	}
}

func TestExecutableCompleter_NotInArgPosition(t *testing.T) {
	c := NewExecutableCompleter()
	ctx := context.Background()

	// Should NOT complete after a space (argument position)
	result, err := c.Complete(ctx, "ls -l", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("expected no completions in argument position, got %d", len(result.Items))
	}
}

func TestExecutableCompleter_AfterPipe(t *testing.T) {
	c := NewExecutableCompleter()
	ctx := context.Background()

	// Should complete command after pipe
	result, err := c.Complete(ctx, "cat file | gr", 13)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have results starting with "gr"
	if len(result.Items) == 0 {
		t.Error("expected completions for 'gr' after pipe")
	}

	// Verify grep is in the results
	found := false
	for _, item := range result.Items {
		if item.Value == "grep" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'grep' in completions after pipe")
	}
}

func TestExecutableCompleter_PathPrefix(t *testing.T) {
	c := NewExecutableCompleter()
	ctx := context.Background()

	// Should NOT complete when prefix contains a path (let file completer handle)
	result, err := c.Complete(ctx, "./scr", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("expected no completions for path prefix, got %d", len(result.Items))
	}
}

func TestExtractPipeContext(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		pos      int
		wantLine string
		wantPos  int
	}{
		{
			name:     "no pipe",
			line:     "ls -la",
			pos:      6,
			wantLine: "ls -la",
			wantPos:  6,
		},
		{
			name:     "single pipe",
			line:     "cat file | grep",
			pos:      15,
			wantLine: "grep",
			wantPos:  4,
		},
		{
			name:     "pipe with spaces",
			line:     "cat file |   gr",
			pos:      15,
			wantLine: "gr",
			wantPos:  2,
		},
		{
			name:     "multiple pipes",
			line:     "cat file | grep foo | wc",
			pos:      24,
			wantLine: "wc",
			wantPos:  2,
		},
		{
			name:     "cursor before pipe",
			line:     "cat f | grep",
			pos:      5,
			wantLine: "cat f | grep",
			wantPos:  5,
		},
		{
			name:     "empty after pipe",
			line:     "cat file | ",
			pos:      11,
			wantLine: "",
			wantPos:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLine, gotPos := ExtractPipeContext(tt.line, tt.pos)
			if gotLine != tt.wantLine {
				t.Errorf("ExtractPipeContext() line = %q, want %q", gotLine, tt.wantLine)
			}
			if gotPos != tt.wantPos {
				t.Errorf("ExtractPipeContext() pos = %d, want %d", gotPos, tt.wantPos)
			}
		})
	}
}
