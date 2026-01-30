package parser

import (
	"testing"
	"unicode/utf8"
)

// FuzzParse tests the Parse function with arbitrary input.
// Run with: go test -fuzz=FuzzParse -fuzztime=30s ./internal/parser/...
func FuzzParse(f *testing.F) {
	// Seed corpus with known patterns
	seeds := []string{
		// Empty and whitespace
		"",
		"   ",
		"\t\n",

		// Agent prefix patterns
		"??",
		"?? ",
		"?? find large files",
		"??find",
		"?? multi word prompt here",

		// Pipe patterns
		"ls | ?? filter",
		"ls |?? filter",
		"cat file.txt | ?? summarize",
		"kubectl get pods -o json |?? extract names",
		"cmd1 | cmd2 | ?? prompt",

		// Inline patterns
		"git log --format=?? oneline",
		"kubectl get pods --sort-by=?? restart count",
		"cmd ?? prompt",
		"cmd --flag ?? value",
		"cmd --flag=??",

		// Regular commands
		"ls -la",
		"echo hello world",
		"git commit -m 'message'",
		`echo "hello"`,
		"cd /path/to/dir",

		// Edge cases
		"?",
		"???",
		"????",
		"? ?",
		"cmd |",
		"| cmd",
		"||",
		"cmd | | cmd",
		"--??",
		"??--",

		// Unicode
		"?? 找到大文件",
		"echo 日本語 | ?? translate",
		"cmd --flag=?? émojis 🎉",

		// Special characters
		"echo $HOME | ?? explain",
		"ls *.go | ?? count",
		"echo 'test' | ?? 'quoted prompt'",
		`cmd "arg with spaces" ?? prompt`,
		"cmd\t??\tprompt",

		// Long inputs
		"very long command with many arguments --flag1 value1 --flag2 value2 | ?? very long prompt with many words",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Skip invalid UTF-8 (the parser expects valid strings)
		if !utf8.ValidString(input) {
			return
		}

		// The main invariant: Parse should never panic
		result := Parse(input)

		// Type should always be valid
		if result.Type < CommandTypeEmpty || result.Type > CommandTypeAgentInline {
			t.Errorf("Parse(%q) returned invalid type: %v", input, result.Type)
		}

		// Validate type-specific invariants
		switch result.Type {
		case CommandTypeEmpty:
			// Empty type should have no command or prompt
			if result.Command != "" || result.AgentPrompt != "" {
				t.Errorf("Parse(%q) = Empty but has content: cmd=%q, prompt=%q",
					input, result.Command, result.AgentPrompt)
			}

		case CommandTypeRegular:
			// Regular commands should have a command but no agent prompt
			if result.Command == "" {
				t.Errorf("Parse(%q) = Regular but has empty command", input)
			}
			if result.AgentPrompt != "" {
				t.Errorf("Parse(%q) = Regular but has agent prompt: %q",
					input, result.AgentPrompt)
			}

		case CommandTypeAgent:
			// Agent prefix: no command, may have prompt
			if result.Command != "" {
				t.Errorf("Parse(%q) = Agent but has command: %q",
					input, result.Command)
			}

		case CommandTypeAgentPipe:
			// Pipe to agent: must have command (the piped command)
			if result.Command == "" {
				t.Errorf("Parse(%q) = AgentPipe but has empty command", input)
			}

		case CommandTypeAgentInline:
			// Inline: must have command prefix
			if result.Command == "" {
				t.Errorf("Parse(%q) = AgentInline but has empty command", input)
			}
		}
	})
}

// FuzzParseConsistency ensures Parse is deterministic.
func FuzzParseConsistency(f *testing.F) {
	f.Add("ls | ?? filter")
	f.Add("?? find files")
	f.Add("cmd --flag=?? value")

	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			return
		}

		// Parse the same input twice
		result1 := Parse(input)
		result2 := Parse(input)

		// Results must be identical
		if result1.Type != result2.Type {
			t.Errorf("Parse(%q) not deterministic: type %v vs %v",
				input, result1.Type, result2.Type)
		}
		if result1.Command != result2.Command {
			t.Errorf("Parse(%q) not deterministic: cmd %q vs %q",
				input, result1.Command, result2.Command)
		}
		if result1.AgentPrompt != result2.AgentPrompt {
			t.Errorf("Parse(%q) not deterministic: prompt %q vs %q",
				input, result1.AgentPrompt, result2.AgentPrompt)
		}
	})
}
