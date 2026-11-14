// internal/compat/prompt_test.go
package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatWelcomePromptFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("alias ll='ls -la'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := FormatWelcomePromptFiles(ShellFiles{Shell: "zsh", RCFile: rc})

	for _, want := range []string{"Welcome to Hash", "zsh", "~/.zshrc", "load"} {
		if !strings.Contains(output, want) {
			t.Errorf("welcome prompt lacks %q:\n%s", want, output)
		}
	}
}

func TestFormatImportSummary(t *testing.T) {
	report := NewReport("~/.zshrc", "zsh")
	report.AddImported(ItemAlias, "ll", "ls -la")
	report.AddImported(ItemAlias, "gs", "git status")
	report.AddImported(ItemExport, "EDITOR", "vim")
	report.AddSkipped(12, "bindkey", "zsh-specific")

	output := FormatImportSummary(report, false)

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

// A sibling of the home directory that shares its prefix is not under it.
func TestFormat_HomePrefixIsNotHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "assaf"))
	other := filepath.Join(root, "assafx", ".zshrc")
	mine := filepath.Join(root, "assaf", ".zshrc")
	for _, p := range []string{other, mine} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, "")
	}
	welcome := FormatWelcomePromptFiles(ShellFiles{Shell: "zsh", RCFile: other})
	header := FormatHashrcCommentFiles([]string{other, mine})
	notice := FormatChangeNotice([]string{other}, 0)
	for name, got := range map[string]string{"welcome": welcome, "header": header, "notice": notice} {
		if strings.Contains(got, "~x") || !strings.Contains(got, other) {
			t.Errorf("%s mangles %s:\n%s", name, other, got)
		}
	}
	if !strings.Contains(header, "~/.zshrc") {
		t.Errorf("header should abbreviate %s:\n%s", mine, header)
	}
}

// The import summary writes the home directory as ~, as the skipped items
// and every other migration message do.
func TestFormatImportSummary_WritesHomeAsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := FormatImportSummary(NewReport(filepath.Join(home, ".zshrc"), "zsh"), false)
	if !strings.Contains(got, "Loaded from ~/.zshrc:") {
		t.Errorf("summary = %q, want the file as ~/.zshrc", got)
	}
}

// The summary of a migration of several files writes each as ~: the home
// was abbreviated in their joined names, the first one only.
func TestFormatImportSummary_WritesEveryFileAsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{".zprofile", ".zshrc"} {
		writeFile(t, filepath.Join(home, name), "export A=1\n")
	}
	_, report, errs := FilterFiles([]string{filepath.Join(home, ".zprofile"), filepath.Join(home, ".zshrc")}, "zsh", "bash")
	if len(errs) != 0 || report == nil {
		t.Fatalf("FilterFiles: %v", errs)
	}
	if got := FormatImportSummary(report, false); !strings.Contains(got, "Loaded from ~/.zprofile + ~/.zshrc:") {
		t.Errorf("summary = %q, want both files as ~", got)
	}
}

// The welcome offers to load from the shell it did not detect.
func TestFormatWelcomePromptFiles_OffersTheOtherShell(t *testing.T) {
	for shell, other := range map[string]string{"bash": "zsh", "zsh": "bash"} {
		got := FormatWelcomePromptFiles(ShellFiles{Shell: shell})
		if !strings.Contains(got, "hash migrate --from "+other+")") {
			t.Errorf("welcome for %s = %q, want it to offer --from %s", shell, got, other)
		}
	}
}

// The import summary says which files did not load.
func TestFormatImportSummary_SaysWhatDidNotLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".bashrc")
	writeFile(t, path, "alias k=kubectl\n}\n")
	_, report, err := FilterWithDialect(path, "bash", "bash")
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatImportSummary(report, false); !strings.Contains(got, "~/.bashrc: not loaded: line 2: ") {
		t.Errorf("summary = %q, want it to say ~/.bashrc did not load", got)
	}
}
