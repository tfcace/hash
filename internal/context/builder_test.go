package context

import (
	"os"
	"testing"
)

func TestBuilder_AutoDetect(t *testing.T) {
	builder := NewBuilder()
	builder.AutoDetect()
	collection := builder.Build()

	// Should have at least cwd
	found := false
	for _, item := range collection.Items {
		if item.Category == CategoryAutoDetect && item.Key == "cwd" {
			found = true
			break
		}
	}
	if !found {
		t.Error("AutoDetect should add cwd")
	}
}

func TestBuilder_WithHistory(t *testing.T) {
	builder := NewBuilder()
	builder.WithHistory([]string{"ls -la", "pwd", "cd .."})
	collection := builder.Build()

	count := 0
	for _, item := range collection.Items {
		if item.Category == CategoryHistory {
			count++
		}
	}
	if count != 3 {
		t.Errorf("WithHistory added %d items, want 3", count)
	}
}

func TestBuilder_WithEnvVars(t *testing.T) {
	// Set a test env var
	os.Setenv("TEST_HASH_VAR", "test_value")
	defer os.Unsetenv("TEST_HASH_VAR")

	builder := NewBuilder()
	builder.WithEnvVars([]string{"TEST_HASH_VAR", "HOME"})
	collection := builder.Build()

	found := false
	for _, item := range collection.Items {
		if item.Category == CategoryEnv && item.Key == "TEST_HASH_VAR" {
			found = true
			if item.Value != "test_value" {
				t.Errorf("Value = %q, want %q", item.Value, "test_value")
			}
		}
	}
	if !found {
		t.Error("WithEnvVars should add TEST_HASH_VAR")
	}
}

func TestBuilder_WithCustom(t *testing.T) {
	builder := NewBuilder()
	builder.WithCustom("note", "This is a custom context item")
	collection := builder.Build()

	found := false
	for _, item := range collection.Items {
		if item.Category == CategoryCustom && item.Key == "note" {
			found = true
		}
	}
	if !found {
		t.Error("WithCustom should add custom item")
	}
}

func TestBuilder_Chaining(t *testing.T) {
	collection := NewBuilder().
		AutoDetect().
		WithHistory([]string{"cmd1", "cmd2"}).
		WithEnvVars([]string{"HOME"}).
		Build()

	if len(collection.Items) == 0 {
		t.Error("Chained builder should produce items")
	}
}

func TestBuilder_HistoryLimit(t *testing.T) {
	history := make([]string, 100)
	for i := range history {
		history[i] = "cmd"
	}

	builder := NewBuilder()
	builder.WithHistoryLimit(history, 10)
	collection := builder.Build()

	count := 0
	for _, item := range collection.Items {
		if item.Category == CategoryHistory {
			count++
		}
	}
	if count != 10 {
		t.Errorf("WithHistoryLimit added %d items, want 10", count)
	}
}
