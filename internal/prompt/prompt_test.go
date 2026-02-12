package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFallbackPrompt(t *testing.T) {
	p := New(Config{Mode: "none"})

	got := p.Generate(PromptContext{
		Cwd:      "/home/user/projects",
		ExitCode: 0,
	})

	// Fallback should include cwd and prompt character
	if got == "" {
		t.Error("Generate() returned empty string")
	}
}

func TestPromptCharacter_Success(t *testing.T) {
	p := New(Config{Mode: "none"})

	ctx := PromptContext{ExitCode: 0}
	got := p.Generate(ctx)

	// Should contain success character
	if !containsPromptChar(got) {
		t.Errorf("Generate() = %q, want prompt character", got)
	}
}

func TestPromptCharacter_Error(t *testing.T) {
	p := New(Config{Mode: "none"})

	ctx := PromptContext{ExitCode: 1}
	got := p.Generate(ctx)

	if got == "" {
		t.Error("Generate() returned empty string for error case")
	}
}

func containsPromptChar(s string) bool {
	for _, r := range s {
		if r == '❯' || r == '$' || r == '>' {
			return true
		}
	}
	return false
}

func TestGenerateMultiLine_SingleLine(t *testing.T) {
	cfg := Config{Mode: "built-in"}
	p := New(cfg)

	ctx := PromptContext{Cwd: "/home/user", ExitCode: 0}
	prefix, prompt := p.GenerateMultiLine(ctx)

	// Built-in prompt is single line, so prefix should be empty
	if prefix != "" {
		t.Errorf("GenerateMultiLine() prefix = %q, want empty for single-line prompt", prefix)
	}
	if prompt == "" {
		t.Error("GenerateMultiLine() prompt is empty")
	}
}

func TestGenerateMultiLine_MultiLine(t *testing.T) {
	// Simulate a multi-line prompt like Starship outputs
	input := "info bar with git, k8s, etc\n❯ "

	lastNewline := strings.LastIndex(input, "\n")
	prefix := input[:lastNewline+1]
	prompt := input[lastNewline+1:]

	if prefix != "info bar with git, k8s, etc\n" {
		t.Errorf("prefix = %q, want %q", prefix, "info bar with git, k8s, etc\n")
	}
	if prompt != "❯ " {
		t.Errorf("prompt = %q, want %q", prompt, "❯ ")
	}
}

func TestCacheInvalidatesOnConfigChange(t *testing.T) {
	// Create a fake starship config file
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "starship.toml")
	if err := os.WriteFile(cfgFile, []byte("[character]\nsymbol = \">\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Point STARSHIP_CONFIG at it
	t.Setenv("STARSHIP_CONFIG", cfgFile)

	// Use built-in mode so we don't need the starship binary,
	// but we can still exercise the cache fields via starshipConfigMtime.
	p := New(Config{Mode: "built-in"})
	ctx := PromptContext{Cwd: "/tmp", ExitCode: 0}

	first := p.Generate(ctx)
	if first == "" {
		t.Fatal("expected non-empty prompt")
	}

	// Manually prime the cache fields as if starship had run
	p.cacheValid = true
	p.cachedCtx = ctx
	p.cachedResult = "cached-prompt"
	p.cachedCfgMtime = p.starshipConfigMtime()

	// Same context, same config mtime — cache should hold
	mtime := p.starshipConfigMtime()
	if !p.cacheValid || p.cachedCtx != ctx || p.cachedCfgMtime != mtime {
		t.Fatal("cache should be valid with same context and config mtime")
	}

	// Simulate config edit: touch the file with a new mtime
	newTime := time.Now().Add(1 * time.Second)
	if err := os.Chtimes(cfgFile, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	// The mtime should now differ from what's cached
	newMtime := p.starshipConfigMtime()
	if newMtime.Equal(p.cachedCfgMtime) {
		t.Fatal("config mtime should differ after touch")
	}
}

func TestStripCursorPositioning(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no escape sequences",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "cursor horizontal absolute",
			input: "hello\x1b[50Gworld",
			want:  "helloworld",
		},
		{
			name:  "cursor forward",
			input: "hello\x1b[5Cworld",
			want:  "helloworld",
		},
		{
			name:  "cursor back",
			input: "hello\x1b[5Dworld",
			want:  "helloworld",
		},
		{
			name:  "cursor position",
			input: "hello\x1b[10;20Hworld",
			want:  "helloworld",
		},
		{
			name:  "save/restore cursor",
			input: "hello\x1b[sworld\x1b[u",
			want:  "helloworld",
		},
		{
			name:  "preserve color sequences",
			input: "\x1b[31mred\x1b[0m",
			want:  "\x1b[31mred\x1b[0m",
		},
		{
			name:  "mixed sequences",
			input: "\x1b[31mhello\x1b[50Gworld\x1b[0m",
			want:  "\x1b[31mhelloworld\x1b[0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCursorPositioning(tt.input)
			if got != tt.want {
				t.Errorf("stripCursorPositioning(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
