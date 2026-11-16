// internal/compat/prompt_test.go
package compat

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/prompt"
)

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain is s without its styling.
func plain(s string) string { return sgr.ReplaceAllString(s, "") }

func TestFormatWelcomePromptFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("alias ll='ls -la'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := plain(FormatWelcomePromptFiles(ShellFiles{Shell: "zsh", RCFile: rc}, prompt.Palette{}))

	want := "┌ migrate · zsh settings found\n" +
		"│ ~/.zshrc\n" +
		"│ Load the settings hash understands? Your files stay as they are.\n" +
		"└─ y load · n start fresh · ? what this does · later: hash migrate --from bash"
	if output != want {
		t.Errorf("got:\n%s\nwant:\n%s", output, want)
	}
}

func TestFormatImportSummary(t *testing.T) {
	report := NewReport("~/.zshrc", "zsh")
	report.AddImported(ItemAlias, "ll", "ls -la")
	report.AddImported(ItemAlias, "gs", "git status")
	report.AddImported(ItemExport, "EDITOR", "vim")
	report.AddSkipped(12, "bindkey", "zsh-specific")

	output := plain(FormatImportSummary(report, false, prompt.Palette{}))

	want := "┌ migrate · loaded from ~/.zshrc\n" +
		"│ ✓ 2 aliases\n" +
		"│ ✓ 1 environment variable\n" +
		"├ skipped 1 item\n" +
		"│ line 12: bindkey\n" +
		"└─ hash migrate status for the full list"
	if output != want {
		t.Errorf("got:\n%s\nwant:\n%s", output, want)
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
	welcome := plain(FormatWelcomePromptFiles(ShellFiles{Shell: "zsh", RCFile: other}, prompt.Palette{}))
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
	got := plain(FormatImportSummary(NewReport(filepath.Join(home, ".zshrc"), "zsh"), false, prompt.Palette{}))
	if !strings.HasPrefix(got, "┌ migrate · loaded from ~/.zshrc\n") {
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
	if got := plain(FormatImportSummary(report, false, prompt.Palette{})); !strings.HasPrefix(got, "┌ migrate · loaded from ~/.zprofile + ~/.zshrc\n") {
		t.Errorf("summary = %q, want both files as ~", got)
	}
}

// The welcome offers to load from the shell it did not detect.
func TestFormatWelcomePromptFiles_OffersTheOtherShell(t *testing.T) {
	for shell, other := range map[string]string{"bash": "zsh", "zsh": "bash"} {
		got := plain(FormatWelcomePromptFiles(ShellFiles{Shell: shell}, prompt.Palette{}))
		if !strings.HasSuffix(got, "later: hash migrate --from "+other) {
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
	if got := plain(FormatImportSummary(report, false, prompt.Palette{})); !strings.Contains(got, "│ ~/.bashrc: not loaded: line 2: ") {
		t.Errorf("summary = %q, want it to say ~/.bashrc did not load", got)
	}
}

// A summary of what loads later says so, and more skipped items than it
// lists are counted.
func TestFormatImportSummary_LaterAndManySkipped(t *testing.T) {
	report := NewReport("/etc/zshrc", "zsh")
	for i := range 5 {
		report.AddSkipped(i+1, fmt.Sprintf("setopt opt%d", i), "zsh-specific")
	}
	got := plain(FormatImportSummary(report, true, prompt.Palette{}))
	want := "┌ migrate · will load from /etc/zshrc\n" +
		"├ skipped 5 items\n" +
		"│ line 1: setopt opt0\n" +
		"│ line 2: setopt opt1\n" +
		"│ line 3: setopt opt2\n" +
		"│ … 2 more\n" +
		"└─ hash migrate status for the full list"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMigrationFrames_ColorsComeFromThePalette(t *testing.T) {
	p := prompt.Palette{Primary: "#010203", Success: "#040506", Dim: "8"}
	report := NewReport("/etc/zshrc", "zsh")
	report.AddImported(ItemAlias, "ll", "ls -la")
	for name, got := range map[string]string{
		"welcome": FormatWelcomePromptFiles(ShellFiles{Shell: "zsh"}, p),
		"summary": FormatImportSummary(report, false, p),
	} {
		if !strings.Contains(got, "38;2;1;2;3mmigrate") || !strings.Contains(got, "\x1b[90m┌") {
			t.Errorf("%s: title is not in the accent on gray chrome: %q", name, got)
		}
	}
	if got := FormatImportSummary(report, false, p); !strings.Contains(got, "38;2;4;5;6m✓") {
		t.Errorf("no success-colored mark: %q", got)
	}
}

// What a config file's skipped line says is drawn inert.
func TestFormatImportSummary_DrawsSkippedLinesInert(t *testing.T) {
	report := NewReport("/etc/zshrc", "zsh")
	report.AddSkipped(1, "x\x1b]0;pwned\x07y", "zsh-specific")
	if got := plain(FormatImportSummary(report, false, prompt.Palette{})); strings.Contains(got, "\x1b]0;pwned") || strings.Contains(got, "\x07") {
		t.Errorf("drawn raw: %q", got)
	}
}
