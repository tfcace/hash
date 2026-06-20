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
