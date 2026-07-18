package shell

import (
	"errors"
	"testing"

	"github.com/tfcace/hash/internal/config"
)

func found(string) (string, error)   { return "/usr/local/bin/x", nil }
func missing(string) (string, error) { return "", errors.New("not found") }

func TestAgentAvailable_StdioNeedsCommandOnPath(t *testing.T) {
	s := &Shell{config: config.Default()} // default: stdio, command claude-agent-acp
	if s.agentAvailable(missing) {
		t.Error("want unavailable when stdio command is not on PATH")
	}
	if !s.agentAvailable(found) {
		t.Error("want available when stdio command resolves on PATH")
	}
}

func TestAgentAvailable_HTTPNeedsURL(t *testing.T) {
	withURL := &Shell{config: &config.Config{Agent: config.AgentConfig{Transport: "http", URL: "http://localhost:11434"}}}
	if !withURL.agentAvailable(missing) {
		t.Error("http transport with a URL should be available regardless of PATH")
	}
	noURL := &Shell{config: &config.Config{Agent: config.AgentConfig{Transport: "http"}}}
	if noURL.agentAvailable(found) {
		t.Error("http transport without a URL should be unavailable")
	}
}

func TestAgentAvailable_EmptyCommandUnavailable(t *testing.T) {
	s := &Shell{config: &config.Config{Agent: config.AgentConfig{Transport: "stdio"}}}
	if s.agentAvailable(found) {
		t.Error("empty command should be unavailable even if lookPath succeeds")
	}
}

// A Command may embed a subcommand as a single string (e.g. "cursor-agent acp"
// or "gemini --experimental-acp"). The transport splits it before spawning, so
// availability must resolve only the program name — not the whole string, which
// is never on PATH. Regression for `??` reporting "No AI agent available" while
// the `model` builtin (which actually connects) worked.
func TestAgentAvailable_EmbeddedSubcommandResolvesProgram(t *testing.T) {
	s := &Shell{config: &config.Config{Agent: config.AgentConfig{Transport: "stdio", Command: "cursor-agent acp"}}}

	// lookPath should be asked for the program only, never the full string.
	onlyProgram := func(name string) (string, error) {
		if name != "cursor-agent" {
			t.Errorf("lookPath called with %q, want %q", name, "cursor-agent")
			return "", errors.New("unexpected lookup")
		}
		return "/usr/local/bin/cursor-agent", nil
	}
	if !s.agentAvailable(onlyProgram) {
		t.Error("want available when the embedded-subcommand program resolves on PATH")
	}
}
