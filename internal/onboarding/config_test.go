package onboarding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAgentConfigCreatesStdioBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := WriteAgentConfig(path, Agent{Command: "claude-agent-acp"}); err != nil {
		t.Fatalf("WriteAgentConfig: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(got)
	for _, want := range []string{"[agent]", `transport = "stdio"`, `command = "claude-agent-acp"`} {
		if !strings.Contains(s, want) {
			t.Errorf("config missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "args") {
		t.Errorf("no-arg agent should not emit args line:\n%s", s)
	}
}

func TestWriteAgentConfigEmitsArgsArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := WriteAgentConfig(path, Agent{Command: "gemini", Args: []string{"--experimental-acp"}}); err != nil {
		t.Fatalf("WriteAgentConfig: %v", err)
	}
	got, _ := os.ReadFile(path)
	if want := `args = ["--experimental-acp"]`; !strings.Contains(string(got), want) {
		t.Errorf("config missing %q:\n%s", want, got)
	}
}

func TestWriteAgentConfigNeverClobbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("# my settings\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAgentConfig(path, Agent{Command: "claude-agent-acp"}); err == nil {
		t.Fatal("expected error writing over existing config, got nil")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "# my settings\n" {
		t.Errorf("existing config was modified: %q", got)
	}
}

func TestAgentConfigured(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "none.toml")
	if AgentConfigured(missing) {
		t.Error("AgentConfigured = true for missing file")
	}
	configured := filepath.Join(dir, "config.toml")
	_ = WriteAgentConfig(configured, Agent{Command: "claude-agent-acp"})
	if !AgentConfigured(configured) {
		t.Error("AgentConfigured = false after writing an [agent] block")
	}
}
