package agent

import (
	"bufio"
	"io"
	"testing"
	"time"
)

func TestACPTransport_New(t *testing.T) {
	cfg := ACPConfig{
		Command: "claude-code-acp",
		Args:    []string{},
	}

	transport := NewACPTransport(cfg)
	if transport == nil {
		t.Fatal("NewACPTransport() returned nil")
	}
	if transport.Name() != "acp" {
		t.Errorf("Name() = %q, want %q", transport.Name(), "acp")
	}
}

func TestACPTransport_ImplementsInterface(t *testing.T) {
	var _ Transport = (*ACPTransport)(nil)
}

func TestACPConfig_ParsedCommand(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		args        []string
		wantProgram string
		wantArgs    []string
	}{
		{
			name:        "simple command",
			command:     "gemini",
			args:        nil,
			wantProgram: "gemini",
			wantArgs:    nil,
		},
		{
			name:        "command with embedded args",
			command:     "gemini --experimental-acp",
			args:        nil,
			wantProgram: "gemini",
			wantArgs:    []string{"--experimental-acp"},
		},
		{
			name:        "command with embedded args and explicit args",
			command:     "gemini --experimental-acp",
			args:        []string{"--model", "gemini-pro"},
			wantProgram: "gemini",
			wantArgs:    []string{"--experimental-acp", "--model", "gemini-pro"},
		},
		{
			name:        "command with multiple embedded args",
			command:     "my-agent --flag1 --flag2 value",
			args:        nil,
			wantProgram: "my-agent",
			wantArgs:    []string{"--flag1", "--flag2", "value"},
		},
		{
			name:        "command with only explicit args",
			command:     "claude",
			args:        []string{"--chat"},
			wantProgram: "claude",
			wantArgs:    []string{"--chat"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ACPConfig{
				Command: tt.command,
				Args:    tt.args,
			}

			program, args := cfg.ParsedCommand()

			if program != tt.wantProgram {
				t.Errorf("ParsedCommand() program = %q, want %q", program, tt.wantProgram)
			}

			if len(args) != len(tt.wantArgs) {
				t.Errorf("ParsedCommand() args = %v, want %v", args, tt.wantArgs)
				return
			}

			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("ParsedCommand() args[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestACPTransport_ReadLoopClosesOnlyOriginalChannels(t *testing.T) {
	transport := NewACPTransport(ACPConfig{Command: "test"})

	pr, pw := io.Pipe()
	reader := bufio.NewReader(pr)
	originalMessages := make(chan []byte, 1)
	originalDone := make(chan struct{})
	replacementMessages := make(chan []byte, 1)
	replacementDone := make(chan struct{})

	transport.reader = reader
	transport.messages = replacementMessages
	transport.done = replacementDone

	go transport.readLoop(reader, originalMessages, originalDone)

	// Simulate connection reset while the old read loop is still running.
	transport.reader = nil
	transport.messages = replacementMessages
	transport.done = replacementDone

	if _, err := pw.Write([]byte("{\"jsonrpc\":\"2.0\"}\n")); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}

	select {
	case <-originalDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for original read loop to exit")
	}

	select {
	case msg, ok := <-originalMessages:
		if !ok {
			t.Fatal("original messages channel closed before delivering buffered message")
		}
		if string(msg) != "{\"jsonrpc\":\"2.0\"}\n" {
			t.Fatalf("unexpected message %q", string(msg))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for original message")
	}

	select {
	case _, ok := <-originalMessages:
		if ok {
			t.Fatal("expected original messages channel to be closed after draining")
		}
	default:
		t.Fatal("expected original messages channel to be closed")
	}

	select {
	case <-replacementDone:
		t.Fatal("replacement done channel should stay open")
	default:
	}

	select {
	case _, ok := <-replacementMessages:
		if !ok {
			t.Fatal("replacement messages channel should stay open")
		}
		t.Fatal("replacement messages channel should remain unused")
	default:
	}
}
