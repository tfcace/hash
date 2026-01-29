// internal/compat/builtins_test.go
package compat

import (
	"testing"
)

func TestNoopBuiltins_List(t *testing.T) {
	builtins := NoopBuiltins()

	expected := []string{"bindkey", "setopt", "unsetopt", "autoload", "compdef", "zstyle"}
	for _, name := range expected {
		if _, ok := builtins[name]; !ok {
			t.Errorf("expected %q in NoopBuiltins", name)
		}
	}
}

func TestNoopBuiltin_Execute(t *testing.T) {
	builtins := NoopBuiltins()
	report := NewReport("~/.zshrc", "zsh")

	// Execute bindkey - should succeed and log
	fn := builtins["bindkey"]
	err := fn([]string{"^R", "history-search"}, report)
	if err != nil {
		t.Errorf("bindkey execution error = %v", err)
	}

	// Should have logged the skip
	if report.Summary.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", report.Summary.Skipped)
	}
}
