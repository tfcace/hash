package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFirstWord tests the firstWord helper function.
func TestFirstWord(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"hello", "hello"},
		{"hello world", "hello"},
		{"  leading spaces", "leading"},
		{"tabs\there", "tabs"},
		{"  multiple   spaces   between", "multiple"},
		{"alias ll='ls -la'", "alias"},
		{"export FOO=bar", "export"},
		{"\t\n\r", ""},
	}

	for _, tt := range tests {
		got := firstWord(tt.input)
		if got != tt.want {
			t.Errorf("firstWord(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestShouldSkipLine tests all conditions in shouldSkipLine.
func TestShouldSkipLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantReason string
		wantSkip   bool
	}{
		// Should not skip
		{"empty line", "", "", false},
		{"normal alias", "alias ll='ls -la'", "", false},
		{"export", "export EDITOR=vim", "", false},
		{"comment", "# comment", "", false},
		{"function def", "myfunc() { echo hi; }", "", false},

		// Starship
		{"starship init zsh", `eval "$(starship init zsh)"`, "starship init", true},
		{"starship init bash", `eval "$(starship init bash)"`, "starship init", true},

		// Zoxide
		{"zoxide init zsh", `eval "$(zoxide init zsh)"`, "zoxide init zsh", true},
		{"zoxide init bash ok", `eval "$(zoxide init bash)"`, "", false}, // bash is ok

		// fzf
		{"fzf --zsh", `source /opt/homebrew/opt/fzf/shell/key-bindings.zsh && source <(fzf --zsh)`, "fzf zsh", true},
		{"fzf --bash ok", `source <(fzf --bash)`, "", false},

		// direnv
		{"direnv hook zsh", `eval "$(direnv hook zsh)"`, "direnv hook zsh", true},
		{"direnv hook bash ok", `eval "$(direnv hook bash)"`, "", false},

		// Zsh plugins
		{"zsh-autosuggestions", `source ~/.zsh/zsh-autosuggestions/zsh-autosuggestions.zsh`, "zsh-autosuggestions", true},
		{"zsh-syntax-highlighting", `source ~/.zsh/zsh-syntax-highlighting/zsh-syntax-highlighting.zsh`, "zsh-syntax-highlighting", true},
		{"zsh-completions", `fpath=(~/.zsh/zsh-completions/src $fpath)`, "zsh-completions", true},

		// Bun
		{"bun completions", `[ -s "$HOME/.bun/_bun" ] && source "$HOME/.bun/_bun"`, "bun zsh completions", true},
		{"bun _bun path", `source ~/.bun/_bun`, "bun zsh completions", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := shouldSkipLine(tt.line)
			gotSkip := reason != ""

			if gotSkip != tt.wantSkip {
				t.Errorf("shouldSkipLine(%q) skip = %v, want %v", tt.line, gotSkip, tt.wantSkip)
			}
			if tt.wantSkip && !strings.Contains(reason, tt.wantReason) {
				t.Errorf("shouldSkipLine(%q) reason = %q, want containing %q", tt.line, reason, tt.wantReason)
			}
		})
	}
}

// TestTruncate tests the truncate helper in prompt.go.
// Note: truncate assumes maxLen >= 4 when truncation is needed.
// Smaller maxLen values with long strings would panic.
func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},       // under limit
		{"hello", 5, "hello"},        // at limit
		{"hello world", 8, "hello..."}, // over limit
		{"hello world", 4, "h..."},   // minimum with ellipsis
		{"", 10, ""},                 // empty string
		{"a", 1, "a"},                // single char under limit
		{"ab", 2, "ab"},              // at limit
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

// TestFormatChangeNotice tests the change notice formatter.
func TestFormatChangeNotice(t *testing.T) {
	tests := []struct {
		rcFile       string
		skipped      int
		wantContains []string
	}{
		{"/home/user/.zshrc", 5, []string{".zshrc", "5", "changed"}},
		{"/home/user/.bashrc", 0, []string{".bashrc", "0"}},
		{"~/.zshrc", 10, []string{".zshrc", "10", "skipped"}},
	}

	for _, tt := range tests {
		got := FormatChangeNotice(tt.rcFile, tt.skipped)
		for _, want := range tt.wantContains {
			if !strings.Contains(got, want) {
				t.Errorf("FormatChangeNotice(%q, %d) = %q, want containing %q", tt.rcFile, tt.skipped, got, want)
			}
		}
	}
}

// TestDetectEnvFile tests env file detection.
func TestDetectEnvFile(t *testing.T) {
	home := "/home/testuser"

	tests := []struct {
		shell string
		want  string
	}{
		{"zsh", filepath.Join(home, ".zshenv")},
		{"bash", ""},
		{"fish", ""},
		{"unknown", ""},
	}

	for _, tt := range tests {
		got := detectEnvFile(tt.shell, home)
		if got != tt.want {
			t.Errorf("detectEnvFile(%q, %q) = %q, want %q", tt.shell, home, got, tt.want)
		}
	}
}

// TestShellFromEnv tests shell name extraction from SHELL env.
func TestShellFromEnv(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/bin/zsh", "zsh"},
		{"/bin/bash", "bash"},
		{"/usr/local/bin/zsh", "zsh"},
		{"/usr/bin/fish", "fish"},
		{"/bin/sh", "sh"},
		{"/bin/ksh", "ksh"},
		{"zsh", "zsh"}, // just the name
		{"/custom/path/myshell", "myshell"},
	}

	for _, tt := range tests {
		got := shellFromEnv(tt.input)
		if got != tt.want {
			t.Errorf("shellFromEnv(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestDetectRCFile_Comprehensive tests rc file detection with all cases.
func TestDetectRCFile_Comprehensive(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name     string
		shell    string
		setup    func()
		wantFile string
	}{
		{
			name:     "zsh returns .zshrc",
			shell:    "zsh",
			wantFile: filepath.Join(tmpDir, ".zshrc"),
		},
		{
			name:  "bash with .bashrc",
			shell: "bash",
			setup: func() {
				os.WriteFile(filepath.Join(tmpDir, ".bashrc"), []byte(""), 0644)
			},
			wantFile: filepath.Join(tmpDir, ".bashrc"),
		},
		{
			name:     "bash without .bashrc falls back to .bash_profile",
			shell:    "bash",
			wantFile: filepath.Join(tmpDir, ".bash_profile"),
		},
		{
			name:     "unknown shell returns empty",
			shell:    "fish",
			wantFile: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up files from previous test
			os.Remove(filepath.Join(tmpDir, ".bashrc"))
			os.Remove(filepath.Join(tmpDir, ".bash_profile"))

			if tt.setup != nil {
				tt.setup()
			}

			got := detectRCFile(tt.shell, tmpDir)
			if got != tt.wantFile {
				t.Errorf("detectRCFile(%q, %q) = %q, want %q", tt.shell, tmpDir, got, tt.wantFile)
			}
		})
	}
}

// TestDetectProfileFile_Comprehensive tests profile file detection with all cases.
func TestDetectProfileFile_Comprehensive(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name     string
		shell    string
		setup    func()
		wantFile string
	}{
		{
			name:     "zsh returns .zprofile",
			shell:    "zsh",
			wantFile: filepath.Join(tmpDir, ".zprofile"),
		},
		{
			name:  "bash with .bash_profile",
			shell: "bash",
			setup: func() {
				os.WriteFile(filepath.Join(tmpDir, ".bash_profile"), []byte(""), 0644)
			},
			wantFile: filepath.Join(tmpDir, ".bash_profile"),
		},
		{
			name:     "bash without .bash_profile falls back to .profile",
			shell:    "bash",
			wantFile: filepath.Join(tmpDir, ".profile"),
		},
		{
			name:     "unknown shell returns empty",
			shell:    "fish",
			wantFile: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up files from previous test
			os.Remove(filepath.Join(tmpDir, ".bash_profile"))
			os.Remove(filepath.Join(tmpDir, ".profile"))

			if tt.setup != nil {
				tt.setup()
			}

			got := detectProfileFile(tt.shell, tmpDir)
			if got != tt.wantFile {
				t.Errorf("detectProfileFile(%q, %q) = %q, want %q", tt.shell, tmpDir, got, tt.wantFile)
			}
		})
	}
}

// TestShellFiles_Files tests the Files method ordering.
func TestShellFiles_Files_Order(t *testing.T) {
	tmpDir := t.TempDir()

	// Create all three files
	envFile := filepath.Join(tmpDir, ".zshenv")
	profileFile := filepath.Join(tmpDir, ".zprofile")
	rcFile := filepath.Join(tmpDir, ".zshrc")

	os.WriteFile(envFile, []byte(""), 0644)
	os.WriteFile(profileFile, []byte(""), 0644)
	os.WriteFile(rcFile, []byte(""), 0644)

	sf := ShellFiles{
		Shell:       "zsh",
		EnvFile:     envFile,
		ProfileFile: profileFile,
		RCFile:      rcFile,
	}

	files := sf.Files()

	if len(files) != 3 {
		t.Fatalf("Files() returned %d files, want 3", len(files))
	}

	// Order should be: env, profile, rc
	if files[0] != envFile {
		t.Errorf("files[0] = %q, want env file %q", files[0], envFile)
	}
	if files[1] != profileFile {
		t.Errorf("files[1] = %q, want profile file %q", files[1], profileFile)
	}
	if files[2] != rcFile {
		t.Errorf("files[2] = %q, want rc file %q", files[2], rcFile)
	}
}

// TestShellFiles_Files_OnlyExisting tests that only existing files are returned.
func TestShellFiles_Files_OnlyExisting(t *testing.T) {
	tmpDir := t.TempDir()

	// Only create rc file
	rcFile := filepath.Join(tmpDir, ".zshrc")
	os.WriteFile(rcFile, []byte(""), 0644)

	sf := ShellFiles{
		Shell:       "zsh",
		EnvFile:     filepath.Join(tmpDir, ".zshenv"),     // doesn't exist
		ProfileFile: filepath.Join(tmpDir, ".zprofile"),   // doesn't exist
		RCFile:      rcFile,
	}

	files := sf.Files()

	if len(files) != 1 {
		t.Fatalf("Files() returned %d files, want 1 (only existing)", len(files))
	}
	if files[0] != rcFile {
		t.Errorf("files[0] = %q, want %q", files[0], rcFile)
	}
}

// TestReport_AddImported_AllTypes tests tracking all types of imported items.
func TestReport_AddImported_AllTypes(t *testing.T) {
	r := NewReport("/test/.zshrc", "zsh")

	r.AddImported(ItemAlias, "ll", "ls -la")
	r.AddImported(ItemAlias, "gs", "git status")
	r.AddImported(ItemExport, "EDITOR", "vim")
	r.AddImported(ItemFunction, "myfunc", "...")

	if r.Summary.Aliases != 2 {
		t.Errorf("Aliases = %d, want 2", r.Summary.Aliases)
	}
	if r.Summary.Exports != 1 {
		t.Errorf("Exports = %d, want 1", r.Summary.Exports)
	}
	if r.Summary.Functions != 1 {
		t.Errorf("Functions = %d, want 1", r.Summary.Functions)
	}

	// Check imported items are tracked
	if len(r.ImportedItems) != 4 {
		t.Errorf("ImportedItems len = %d, want 4", len(r.ImportedItems))
	}
}

// TestReport_AddSkipped_LineNumbers tests tracking skipped items with line numbers.
func TestReport_AddSkipped_LineNumbers(t *testing.T) {
	r := NewReport("/test/.zshrc", "zsh")

	r.AddSkipped(10, "bindkey '^R' history", "zsh-specific")
	r.AddSkipped(20, "setopt AUTO_CD", "zsh-specific")

	if r.Summary.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", r.Summary.Skipped)
	}
	if len(r.SkippedItems) != 2 {
		t.Errorf("SkippedItems len = %d, want 2", len(r.SkippedItems))
	}

	// Check line numbers
	if r.SkippedItems[0].Line != 10 {
		t.Errorf("SkippedItems[0].Line = %d, want 10", r.SkippedItems[0].Line)
	}
	if r.SkippedItems[1].Line != 20 {
		t.Errorf("SkippedItems[1].Line = %d, want 20", r.SkippedItems[1].Line)
	}
}

// TestFormatImportSummary_Contents tests the import summary content.
func TestFormatImportSummary_Contents(t *testing.T) {
	r := NewReport("/home/user/.zshrc", "zsh")
	r.AddImported(ItemAlias, "ll", "ls -la")
	r.AddImported(ItemExport, "EDITOR", "vim")
	r.AddSkipped(10, "bindkey '^R' history", "zsh-specific")

	summary := FormatImportSummary(r)

	// Should contain source file
	if !strings.Contains(summary, ".zshrc") {
		t.Error("Summary should contain source file")
	}

	// Should mention aliases
	if !strings.Contains(summary, "alias") {
		t.Error("Summary should mention aliases")
	}

	// Should mention skipped
	if !strings.Contains(summary, "kipped") { // "Skipped" or "skipped"
		t.Error("Summary should mention skipped items")
	}
}

// TestFormatImportSummary_ManySkipped tests truncation of skipped items.
func TestFormatImportSummary_ManySkipped(t *testing.T) {
	r := NewReport("/home/user/.zshrc", "zsh")
	// Add more than 3 skipped items
	for i := 0; i < 10; i++ {
		r.AddSkipped(i+1, "skipped line", "reason")
	}

	summary := FormatImportSummary(r)

	// Should mention "more"
	if !strings.Contains(summary, "more") {
		t.Error("Summary with many skipped items should mention 'more'")
	}
}
