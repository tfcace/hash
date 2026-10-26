package prompt

import (
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

// The cached prompt's clock must be today's time. Starship merges the time
// segment's colors with its neighbor's in the full prompt, so the bytes
// `starship module time` prints alone never appear in it: patching by those
// bytes left the clock frozen at precompute time.
func TestTryPrecomputed_PatchesClockInMergedSegment(t *testing.T) {
	stale := time.Now().Add(-90 * time.Minute).Format("15:04")
	p := New(Config{Mode: "none"})
	p.precomputed = &precomputeResult{
		cwd:     "/repo",
		output:  "\x1b[48;2;26;31;27;38;2;31;46;43m\x1b[38;2;224;240;239m  " + stale + " \x1b[0m\n",
		timeStr: stale,
	}

	before := time.Now().Format("15:04")
	got, ok := p.TryPrecomputed(PromptContext{Cwd: "/repo"})
	after := time.Now().Format("15:04")
	if !ok {
		t.Fatal("TryPrecomputed() missed a matching precompute")
	}
	if strings.Contains(got, stale) {
		t.Errorf("prompt still shows the precompute time %s: %q", stale, got)
	}
	if !strings.Contains(got, before) && !strings.Contains(got, after) {
		t.Errorf("prompt does not show the current time: %q", got)
	}
}
