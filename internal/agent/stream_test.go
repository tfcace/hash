package agent

import (
	"context"
	"testing"
)

func TestStreamCollector_Empty(t *testing.T) {
	c := NewStreamCollector()

	resp := c.Response()
	if resp.Type != ResponseTypeError {
		t.Errorf("expected ResponseTypeError, got %v", resp.Type)
	}
}

func TestStreamCollector_Command(t *testing.T) {
	c := NewStreamCollector()
	c.Append("find . -name ")
	c.Append("'*.go'")

	if c.Text() != "find . -name '*.go'" {
		t.Errorf("expected 'find . -name '*.go'', got %q", c.Text())
	}

	resp := c.Response()
	if resp.Type != ResponseTypeCommand {
		t.Errorf("expected ResponseTypeCommand, got %v", resp.Type)
	}
	if resp.Command != "find . -name '*.go'" {
		t.Errorf("expected command 'find . -name '*.go'', got %q", resp.Command)
	}
}

func TestStreamCollector_Explanation(t *testing.T) {
	c := NewStreamCollector()
	c.Append("This is a long explanation about how to use the find command. ")
	c.Append("It searches for files matching a pattern.")

	resp := c.Response()
	if resp.Type != ResponseTypeExplanation {
		t.Errorf("expected ResponseTypeExplanation, got %v", resp.Type)
	}
}

func TestClient_StreamRequest_Fallback(t *testing.T) {
	// Create a mock transport that doesn't support streaming
	mock := NewMockTransport(Response{
		Type:    ResponseTypeCommand,
		Command: "ls -la",
	})

	client := NewClient(mock)
	ctx := context.Background()

	textCh, errCh := client.StreamRequest(ctx, Request{Prompt: "list files"})

	// Collect text
	var text string
	for chunk := range textCh {
		text += chunk
	}

	// Check for errors
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	default:
	}

	if text != "ls -la" {
		t.Errorf("expected 'ls -la', got %q", text)
	}
}

func TestAgentError(t *testing.T) {
	err := &AgentError{Message: "test error"}
	if err.Error() != "test error" {
		t.Errorf("expected 'test error', got %q", err.Error())
	}
}

func TestLooksLikeCommand(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		{"find . -type f -size +100M", true},
		{"git push origin main", true},
		{"The largest files are config.db and logs.tar", false},
		{"ls -la | sort -k5 -h | head", true},
		{"This is a multi-line\nexplanation of how to do something.", false},
		// Contractions and conversational patterns
		{"I'll convert the CSV data to JSON format for you.", false},
		{"I'm going to list the files", false},
		{"I've found 5 matching files", false},
		{"Here's the command you need", false},
		{"Here is what you should run", false},
		{"Let me show you how to do this", false},
		{"Looking at the output", false},
		{"You can use grep to filter", false},
		{"This will list all files", false},
	}
	for _, tt := range tests {
		name := tt.text
		if len(name) > 20 {
			name = name[:20]
		}
		t.Run(name, func(t *testing.T) {
			got := looksLikeCommand(tt.text)
			if got != tt.expected {
				t.Errorf("looksLikeCommand(%q) = %v, want %v", tt.text, got, tt.expected)
			}
		})
	}
}

func TestProcessAgentResponse(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantText     string
		wantExpects  bool
	}{
		{
			name:        "no marker",
			input:       "Here is your answer.",
			wantText:    "Here is your answer.",
			wantExpects: false,
		},
		{
			name:        "with marker at end",
			input:       "Would you like more details?\n[AWAITING_INPUT]",
			wantText:    "Would you like more details?",
			wantExpects: true,
		},
		{
			name:        "marker with trailing whitespace",
			input:       "Choose an option:\n[AWAITING_INPUT]\n  ",
			wantText:    "Choose an option:",
			wantExpects: true,
		},
		{
			name:        "marker in middle is not stripped",
			input:       "Use [AWAITING_INPUT] to signal.\nDone.",
			wantText:    "Use [AWAITING_INPUT] to signal.\nDone.",
			wantExpects: false,
		},
		{
			name:        "empty after marker stripped",
			input:       "[AWAITING_INPUT]",
			wantText:    "",
			wantExpects: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotExpects := ProcessAgentResponse(tt.input)
			if gotText != tt.wantText {
				t.Errorf("text = %q, want %q", gotText, tt.wantText)
			}
			if gotExpects != tt.wantExpects {
				t.Errorf("expectsInput = %v, want %v", gotExpects, tt.wantExpects)
			}
		})
	}
}
