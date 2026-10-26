package learning

import (
	"testing"
)

func TestNormalizeError_FileNotFound(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"file 'foo.txt' not found", "file '{file}' not found"},
		{"file \"bar.json\" not found", "file '{file}' not found"},
		{"file /path/to/file.txt not found", "file {path} not found"},
	}

	for _, tt := range tests {
		got := NormalizeError(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeError(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeError_LineNumber(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"line 12: syntax error", "line {n}: syntax error"},
		{"error on line 99", "error on line {n}"},
		{"at line 1", "at line {n}"},
	}

	for _, tt := range tests {
		got := NormalizeError(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeError(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeError_Permission(t *testing.T) {
	input := "permission denied: /usr/local/bin/script.sh"
	want := "permission denied: {path}"

	got := NormalizeError(input)
	if got != want {
		t.Errorf("NormalizeError(%q) = %q, want %q", input, got, want)
	}
}

func TestExtractPattern_CommandError(t *testing.T) {
	pattern := ExtractPattern("./deploy.sh", "bash: ./deploy.sh: Permission denied", 126)

	if pattern.CommandPattern != "{script}" {
		t.Errorf("CommandPattern = %q, want %q", pattern.CommandPattern, "{script}")
	}
	if pattern.ErrorPattern != "permission denied" {
		t.Errorf("ErrorPattern = %q, want %q", pattern.ErrorPattern, "permission denied")
	}
	if pattern.ExitCode != 126 {
		t.Errorf("ExitCode = %d, want %d", pattern.ExitCode, 126)
	}
}

func TestRelated(t *testing.T) {
	tests := []struct {
		failed, fix string
		want        bool
	}{
		{"./deploy.sh", "chmod +x deploy.sh", true},
		{"gti status", "git status", true},
		{"make build", "go build ./...", true},
		{"npm test", "npm install", true},
		{"git checkout msater", "git fetch --all", true},
		{"jj log -r x", "git commit --amend", false},
		{"jj log -r x", "claude", false},
		{"cd nope", "ls", false},
	}
	for _, tt := range tests {
		if got := Related(tt.failed, tt.fix); got != tt.want {
			t.Errorf("Related(%q, %q) = %v, want %v", tt.failed, tt.fix, got, tt.want)
		}
	}
}

// An error that quotes the word the user typed ("unrecognized subcommand
// 'stauts'") is about that word: a fix learned for one typo must not be
// offered for another. Quoted words the user did not type still generalize.
func TestExtractPattern_KeepsQuotedWordFromCommand(t *testing.T) {
	stauts := ExtractPattern("jj stauts", "error: unrecognized subcommand 'stauts'\n\n  tip: a similar subcommand exists: 'status'", 2)
	fetch := ExtractPattern("jj fetch", "error: unrecognized subcommand 'fetch'", 2)
	if stauts.ErrorPattern == fetch.ErrorPattern {
		t.Errorf("two typos share the signature %q", stauts.ErrorPattern)
	}
	if want := "error: unrecognized subcommand 'stauts'"; stauts.ErrorPattern != want {
		t.Errorf("ErrorPattern = %q, want %q", stauts.ErrorPattern, want)
	}

	a := ExtractPattern("make", "make: *** No rule to make target 'build-a'.  Stop.", 2)
	b := ExtractPattern("make", "make: *** No rule to make target 'build-b'.  Stop.", 2)
	if a.ErrorPattern != b.ErrorPattern {
		t.Errorf("quoted words the user did not type should generalize: %q vs %q", a.ErrorPattern, b.ErrorPattern)
	}
}
