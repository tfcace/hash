package agent

import (
	"testing"
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
