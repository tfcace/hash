package agent

import (
	"context"
	"strings"
	"testing"
)

// TestLooksLikeCommand_EdgeCases tests edge cases for command detection.
func TestLooksLikeCommand_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Empty and whitespace
		{"empty string", "", true},            // short, so considered command
		{"single space", " ", true},           // short
		{"multiple spaces", "     ", true},    // short
		{"tab", "\t", true},                   // short
		{"newline only", "\n", false},         // has newline
		{"carriage return", "\r", true},       // short, no \n
		{"mixed whitespace", " \t \t ", true}, // short

		// Very long inputs
		{"long command-like", strings.Repeat("a", 79), true}, // under 80
		{"exactly 80 chars", strings.Repeat("a", 80), false}, // at threshold
		{"over 80 chars", strings.Repeat("a", 100), false},   // over threshold

		// Unicode edge cases
		{"emoji only", "🚀", true},                       // short
		{"emoji command", "echo 🎉", true},               // starts with echo
		{"japanese text", "これはテストです", true},             // short
		{"arabic text", "مرحبا", true},                  // short, RTL
		{"chinese short", "这是一个很长的解释关于如何使用命令行工具", true}, // 60 bytes, under 80

		// Special characters
		{"null byte", "\x00", true},             // short
		{"control chars", "\x01\x02\x03", true}, // short
		{"backslash", "\\", true},               // short
		{"quotes", `""`, true},                  // short
		{"backticks", "``", true},               // short

		// Command-like patterns
		{"path command", "/usr/bin/python", true},
		{"relative path", "./script.sh", true},
		{"home path", "~/bin/tool", true}, // short, not explanation
		{"pipe", "ls | grep foo", true},
		{"redirect", "echo hello > file", true},
		{"background", "sleep 10 &", true},

		// Tricky explanations
		{"starts with I", "I ls", false},               // starts with "I "
		{"The at start", "The command is ls", false},   // starts with "The "
		{"This at start", "This lists files", false},   // starts with "This "
		{"contains is", "What is the command", false},  // contains " is "
		{"contains are", "Files are listed", false},    // contains " are "
		{"contains will", "It will list files", false}, // contains " will "
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeCommand(tt.input)
			if got != tt.expected {
				t.Errorf("looksLikeCommand(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// TestParseAgentResponse_EdgeCases tests edge cases for response parsing.
func TestParseAgentResponse_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedType ResponseType
	}{
		// Whitespace handling
		{"leading spaces", "  ls -la", ResponseTypeCommand},
		{"trailing spaces", "ls -la  ", ResponseTypeCommand},
		{"leading newlines", "\n\nls -la", ResponseTypeCommand},
		{"only whitespace", "   \t   ", ResponseTypeCommand}, // becomes empty, then short

		// Large inputs
		{"1KB string", strings.Repeat("x", 1024), ResponseTypeExplanation},
		{"10KB string", strings.Repeat("y", 10240), ResponseTypeExplanation},

		// Unicode
		{"unicode command", "echo '日本語'", ResponseTypeCommand},
		{"unicode explanation", "日本語の説明です。これは長い文章です。", ResponseTypeCommand}, // under 80 bytes
		{"mixed unicode", "ls -la # 列出文件", ResponseTypeCommand},

		// Special command formats
		{"sudo command", "sudo rm -rf /tmp/*", ResponseTypeCommand},
		{"env var prefix", "FOO=bar command", ResponseTypeCommand}, // short
		{"subshell", "$(pwd)/script.sh", ResponseTypeCommand},      // starts with / after eval
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := parseAgentResponse(tt.input)
			if resp.Type != tt.expectedType {
				t.Errorf("parseAgentResponse(%q) type = %v, want %v",
					truncate(tt.input, 50), resp.Type, tt.expectedType)
			}
		})
	}
}

// TestBuildPromptWithContext_EdgeCases tests edge cases for prompt building.
func TestBuildPromptWithContext_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		req     Request
		checkFn func(t *testing.T, result string)
	}{
		{
			name: "empty request",
			req:  Request{},
			checkFn: func(t *testing.T, result string) {
				// Empty request produces empty prompt (no context, no prompt text)
				// Just verify it doesn't panic
			},
		},
		{
			name: "prompt only",
			req:  Request{Prompt: "test"},
			checkFn: func(t *testing.T, result string) {
				if !strings.Contains(result, "test") {
					t.Errorf("expected 'test' in result, got %q", result)
				}
			},
		},
		{
			name: "all context fields",
			req: Request{
				Prompt: "find files",
				Context: Context{
					Cwd:         "/home/user",
					GitBranch:   "feature/test",
					KubeContext: "production",
					History:     []string{"ls", "cd /tmp", "pwd"},
					EnvVars:     map[string]string{"HOME": "/home/user", "PATH": "/usr/bin"},
					LastOutput:  "file1.txt\nfile2.txt",
					LastError:   "permission denied",
				},
			},
			checkFn: func(t *testing.T, result string) {
				// Should contain all context
				if !strings.Contains(result, "/home/user") {
					t.Error("expected cwd in result")
				}
				if !strings.Contains(result, "feature/test") {
					t.Error("expected git branch in result")
				}
				if !strings.Contains(result, "production") {
					t.Error("expected kube context in result")
				}
				if !strings.Contains(result, "find files") {
					t.Error("expected prompt in result")
				}
			},
		},
		{
			name: "unicode in context",
			req: Request{
				Prompt: "日本語のプロンプト",
				Context: Context{
					Cwd:       "/home/日本語/プロジェクト",
					GitBranch: "feature/日本語",
				},
			},
			checkFn: func(t *testing.T, result string) {
				if !strings.Contains(result, "日本語") {
					t.Error("expected unicode to be preserved")
				}
			},
		},
		{
			name: "very long history",
			req: Request{
				Prompt: "test",
				Context: Context{
					History: make([]string, 1000),
				},
			},
			checkFn: func(t *testing.T, result string) {
				// Should not panic, should contain prompt
				if !strings.Contains(result, "test") {
					t.Error("expected prompt in result")
				}
			},
		},
		{
			name: "special characters in env vars",
			req: Request{
				Prompt: "test",
				Context: Context{
					EnvVars: map[string]string{
						"SPECIAL": "value with\nnewlines\tand\ttabs",
						"QUOTES":  `"quoted" and 'single'`,
					},
				},
			},
			checkFn: func(t *testing.T, result string) {
				// Should not panic
				if !strings.Contains(result, "test") {
					t.Error("expected prompt in result")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPromptWithContext(tt.req)
			tt.checkFn(t, result)
		})
	}
}

// TestStreamCollector_EdgeCases tests edge cases for stream collection.
func TestStreamCollector_EdgeCases(t *testing.T) {
	t.Run("empty collector", func(t *testing.T) {
		c := NewStreamCollector()
		if c.Text() != "" {
			t.Errorf("expected empty text, got %q", c.Text())
		}
		resp := c.Response()
		if resp.Type != ResponseTypeError {
			t.Errorf("expected error type for empty collector, got %v", resp.Type)
		}
	})

	t.Run("whitespace only", func(t *testing.T) {
		c := NewStreamCollector()
		c.Append("   ")
		c.Append("\t")
		c.Append("\n")
		resp := c.Response()
		// After trim, becomes empty or very short
		if resp.Type != ResponseTypeError && resp.Type != ResponseTypeCommand {
			t.Errorf("unexpected type for whitespace: %v", resp.Type)
		}
	})

	t.Run("many small appends", func(t *testing.T) {
		c := NewStreamCollector()
		for i := 0; i < 10000; i++ {
			c.Append("x")
		}
		text := c.Text()
		if len(text) != 10000 {
			t.Errorf("expected 10000 chars, got %d", len(text))
		}
	})

	t.Run("unicode chunks", func(t *testing.T) {
		c := NewStreamCollector()
		c.Append("日")
		c.Append("本")
		c.Append("語")
		if c.Text() != "日本語" {
			t.Errorf("expected '日本語', got %q", c.Text())
		}
	})

	t.Run("mixed content", func(t *testing.T) {
		c := NewStreamCollector()
		c.Append("Command: ")
		c.Append("find . ")
		c.Append("-name '*.go'")
		text := c.Text()
		if text != "Command: find . -name '*.go'" {
			t.Errorf("unexpected text: %q", text)
		}
	})
}

// TestClient_EdgeCases tests edge cases for the Client.
func TestClient_EdgeCases(t *testing.T) {
	t.Run("empty response from transport", func(t *testing.T) {
		mock := NewMockTransport() // No responses
		client := NewClient(mock)

		ctx := context.Background()
		resp, err := client.Ask(ctx, Request{Prompt: "test"})

		// Should return "no response" error
		if err == nil && resp.Type != ResponseTypeError {
			t.Error("expected error for empty response")
		}
	})

	t.Run("error response", func(t *testing.T) {
		mock := NewMockTransport(Response{
			Type:  ResponseTypeError,
			Error: "agent error",
		})
		client := NewClient(mock)

		ctx := context.Background()
		resp, _ := client.Ask(ctx, Request{Prompt: "test"})

		if resp.Type != ResponseTypeError {
			t.Errorf("expected error type, got %v", resp.Type)
		}
		if resp.Error != "agent error" {
			t.Errorf("expected 'agent error', got %q", resp.Error)
		}
	})

	t.Run("context builder chain", func(t *testing.T) {
		// Test the builder pattern doesn't break
		cb := NewContextBuilder().
			WithHistory([]string{"ls", "pwd"}).
			WithLastOutput("output").
			WithLastError("error").
			WithEnvVars([]string{"PATH", "HOME", "NONEXISTENT"})

		ctx := cb.Build()
		if len(ctx.History) != 2 {
			t.Errorf("expected 2 history items, got %d", len(ctx.History))
		}
	})
}

// TestHTTPTransport_EdgeCases tests edge cases for HTTP transport.
func TestHTTPTransport_EdgeCases(t *testing.T) {
	t.Run("default timeout", func(t *testing.T) {
		transport := NewHTTPTransport(HTTPConfig{
			URL:   "http://localhost:11434/api/generate",
			Model: "codellama",
			// No timeout specified
		})
		if transport == nil {
			t.Fatal("expected non-nil transport")
		}
		if transport.Name() != "http" {
			t.Errorf("expected 'http', got %q", transport.Name())
		}
	})

	t.Run("connect is no-op", func(t *testing.T) {
		transport := NewHTTPTransport(HTTPConfig{
			URL:   "http://localhost:11434/api/generate",
			Model: "codellama",
		})
		err := transport.Connect(context.Background())
		if err != nil {
			t.Errorf("Connect() error = %v", err)
		}
	})

	t.Run("close is no-op", func(t *testing.T) {
		transport := NewHTTPTransport(HTTPConfig{
			URL:   "http://localhost:11434/api/generate",
			Model: "codellama",
		})
		err := transport.Close()
		if err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
}

// TestAgentError_EdgeCases tests edge cases for AgentError.
func TestAgentError_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"empty message", ""},
		{"unicode message", "日本語エラー"},
		{"very long message", strings.Repeat("error ", 1000)},
		{"special chars", "error: 'file' not found\n\tat line 10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &AgentError{Message: tt.message}
			if err.Error() != tt.message {
				t.Errorf("Error() = %q, want %q", err.Error(), tt.message)
			}
		})
	}
}

// truncate is a helper for test output
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
