package completion

import (
	"context"
	"testing"
)

func TestSemanticCompleter_Name(t *testing.T) {
	c := NewSemanticCompleter()
	if c.Name() != "semantic" {
		t.Fatalf("Name() = %q, want %q", c.Name(), "semantic")
	}
}

func TestSemanticCompleter_UnknownCommand_ReturnsEmpty(t *testing.T) {
	c := NewSemanticCompleter()
	result, err := c.Complete(context.Background(), "someunknowncmd arg", len("someunknowncmd arg"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items for unknown command, got %d", len(result.Items))
	}
}

func TestSemanticCompleter_PipeContext(t *testing.T) {
	c := NewSemanticCompleter()
	// Override the ssh handler for testing
	c.handlers["ssh"] = &SSHHandler{
		readFile: func(path string) ([]string, error) {
			return []string{"Host myserver", "Host devbox"}, nil
		},
	}

	line := "cat file | ssh my"
	result, err := c.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].Value != "myserver" {
		t.Errorf("got %q, want %q", result.Items[0].Value, "myserver")
	}
}

func TestSemanticCompleter_CommandPositionOnly(t *testing.T) {
	c := NewSemanticCompleter()
	// Should not complete just the command name
	result, err := c.Complete(context.Background(), "ss", len("ss"))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("should not complete command name, got %d items", len(result.Items))
	}
}
