package learning

import (
	"testing"
	"unicode/utf8"
)

// FuzzNormalizeError tests error normalization with arbitrary input.
// Run with: go test -fuzz=FuzzNormalizeError -fuzztime=30s ./internal/learning/...
func FuzzNormalizeError(f *testing.F) {
	// Seed corpus with realistic error messages
	seeds := []string{
		// Empty
		"",

		// Permission errors
		"bash: ./script.sh: Permission denied",
		"permission denied: '/etc/passwd'",
		"Error: EACCES: permission denied, open '/root/.config'",

		// Not found errors
		"bash: foo: command not found",
		"zsh: command not found: bar",
		"No such file or directory: /path/to/file",
		"/usr/bin/python: No such file or directory",

		// Syntax errors
		"syntax error near unexpected token `)'",
		"SyntaxError: invalid syntax at line 42",
		"parse error at line 10",

		// Connection errors
		"curl: (7) Failed to connect to localhost port 8080: Connection refused",
		"Error: connect ECONNREFUSED 127.0.0.1:3000",
		"timeout: connect timed out",

		// File/directory errors
		"rm: cannot remove 'dir': Is a directory",
		"cd: not a directory: /file.txt",
		"mkdir: cannot create directory 'test': File exists",

		// Paths and filenames
		`Error in "/home/user/project/main.go"`,
		`Failed to load '/var/log/app.log'`,
		"Cannot open /etc/hosts: permission denied",

		// Line numbers
		"error at line 123",
		"main.go:45: undefined: foo",
		"File 'test.py', line 10, in <module>",

		// Complex multi-line
		"error: cannot find module\n  at /home/user/app/index.js:10:5\n  at line 42",

		// Unicode
		"错误: 找不到文件",
		"Erreur: fichier non trouvé",
		"エラー: ファイルが見つかりません",

		// Edge cases
		"'",
		`""`,
		"/",
		"//",
		"line",
		"line 1 line 2 line 3",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			return
		}

		// Should never panic
		result := NormalizeError(input)

		// Result should be valid UTF-8
		if !utf8.ValidString(result) {
			t.Errorf("NormalizeError(%q) produced invalid UTF-8: %q", input, result)
		}

		// Result should be lowercase (the function lowercases)
		for _, r := range result {
			if r >= 'A' && r <= 'Z' {
				t.Errorf("NormalizeError(%q) contains uppercase: %q", input, result)
				break
			}
		}
	})
}

// FuzzExtractPattern tests pattern extraction with arbitrary command/error pairs.
// Run with: go test -fuzz=FuzzExtractPattern -fuzztime=30s ./internal/learning/...
func FuzzExtractPattern(f *testing.F) {
	// Seed corpus: (command, stderr, exitCode)
	type seed struct {
		cmd      string
		stderr   string
		exitCode int
	}

	seeds := []seed{
		{"./script.sh", "Permission denied", 126},
		{"foo", "command not found: foo", 127},
		{"cat /nonexistent", "No such file or directory", 1},
		{"python main.py", "SyntaxError: invalid syntax at line 10", 1},
		{"curl localhost:8080", "Connection refused", 7},
		{"rm -rf /", "Permission denied", 1},
		{"./deploy.sh", "bash: ./deploy.sh: Permission denied", 126},
		{"/usr/local/bin/myapp", "Segmentation fault", 139},
		{"", "", 0},
		{"ls", "", 0},
		{"git push", "error: failed to push", 1},
		// Edge cases: whitespace-only stderr
		{"cmd", "\n", 1},
		{"cmd", "\n\n\n", 1},
		{"cmd", "   ", 1},
		{"cmd", "\t\n", 1},
	}

	for _, s := range seeds {
		f.Add(s.cmd, s.stderr, s.exitCode)
	}

	f.Fuzz(func(t *testing.T, cmd, stderr string, exitCode int) {
		if !utf8.ValidString(cmd) || !utf8.ValidString(stderr) {
			return
		}

		// Should never panic
		pattern := ExtractPattern(cmd, stderr, exitCode)

		// CommandPattern should be valid UTF-8
		if !utf8.ValidString(pattern.CommandPattern) {
			t.Errorf("ExtractPattern command pattern invalid UTF-8: %q", pattern.CommandPattern)
		}

		// ErrorPattern should be valid UTF-8
		if !utf8.ValidString(pattern.ErrorPattern) {
			t.Errorf("ExtractPattern error pattern invalid UTF-8: %q", pattern.ErrorPattern)
		}

		// ExitCode should match input
		if pattern.ExitCode != exitCode {
			t.Errorf("ExtractPattern exit code mismatch: got %d, want %d",
				pattern.ExitCode, exitCode)
		}

		// Non-empty stderr should produce non-empty error pattern
		if stderr != "" && pattern.ErrorPattern == "" {
			t.Errorf("ExtractPattern(%q, %q, %d) produced empty error pattern",
				cmd, stderr, exitCode)
		}
	})
}

// FuzzExtractPatternConsistency ensures pattern extraction is deterministic.
func FuzzExtractPatternConsistency(f *testing.F) {
	f.Add("./script.sh", "Permission denied", 126)
	f.Add("foo", "command not found", 127)

	f.Fuzz(func(t *testing.T, cmd, stderr string, exitCode int) {
		if !utf8.ValidString(cmd) || !utf8.ValidString(stderr) {
			return
		}

		// Extract the same pattern twice
		p1 := ExtractPattern(cmd, stderr, exitCode)
		p2 := ExtractPattern(cmd, stderr, exitCode)

		// Results must be identical
		if p1.CommandPattern != p2.CommandPattern {
			t.Errorf("ExtractPattern not deterministic: cmd pattern %q vs %q",
				p1.CommandPattern, p2.CommandPattern)
		}
		if p1.ErrorPattern != p2.ErrorPattern {
			t.Errorf("ExtractPattern not deterministic: err pattern %q vs %q",
				p1.ErrorPattern, p2.ErrorPattern)
		}
		if p1.ExitCode != p2.ExitCode {
			t.Errorf("ExtractPattern not deterministic: exit code %d vs %d",
				p1.ExitCode, p2.ExitCode)
		}
	})
}
