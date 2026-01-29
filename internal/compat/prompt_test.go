// internal/compat/prompt_test.go
package compat

import (
	"strings"
	"testing"
)

func TestFormatWelcomePrompt(t *testing.T) {
	output := FormatWelcomePrompt("zsh", "~/.zshrc")

	if !strings.Contains(output, "Welcome to Hash") {
		t.Error("expected Welcome message")
	}
	if !strings.Contains(output, "zsh") {
		t.Error("expected shell name")
	}
	if !strings.Contains(output, ".zshrc") {
		t.Error("expected rc file")
	}
	if !strings.Contains(output, "load") {
		t.Error("expected 'load' terminology")
	}
}

func TestFormatImportSummary(t *testing.T) {
	report := NewReport("~/.zshrc", "zsh")
	report.AddImported(ItemAlias, "ll", "ls -la")
	report.AddImported(ItemAlias, "gs", "git status")
	report.AddImported(ItemExport, "EDITOR", "vim")
	report.AddSkipped(12, "bindkey", "zsh-specific")

	output := FormatImportSummary(report)

	if !strings.Contains(output, "Loaded from") {
		t.Errorf("expected 'Loaded from' in output: %s", output)
	}
	if !strings.Contains(output, "2 aliases") {
		t.Errorf("expected '2 aliases' in output: %s", output)
	}
	if !strings.Contains(output, "1 environment") {
		t.Errorf("expected '1 environment' in output: %s", output)
	}
	if !strings.Contains(output, "1 item") || !strings.Contains(output, "Skipped") {
		t.Errorf("expected skipped count in output: %s", output)
	}
}
