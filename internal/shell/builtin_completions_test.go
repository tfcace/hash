package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validKubectlTOML = `[plugin]
name = "kubectl"
commands = ["kubectl"]

[[rules]]
subcommands = ["delete pod", "logs"]
[rules.source]
exec = ["kubectl", "get", "pods", "-o", "name"]
`

func TestCompletionsIsBuiltin(t *testing.T) {
	if !isBuiltin("completions") {
		t.Fatal("completions should be a builtin")
	}
}

func TestExtractPluginTOML(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		want  string
	}{
		{"plain", "[plugin]\nname = \"x\"", "[plugin]\nname = \"x\""},
		{"surrounding whitespace", "\n\n[plugin]\nname = \"x\"\n\n", "[plugin]\nname = \"x\""},
		{"fenced", "Here you go:\n```toml\n[plugin]\nname = \"x\"\n```\nEnjoy!", "[plugin]\nname = \"x\""},
		{"fenced no lang", "```\n[plugin]\n```", "[plugin]"},
		{"only first block", "```\nfirst\n```\n```\nsecond\n```", "first"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractPluginTOML(tt.reply); got != tt.want {
				t.Errorf("extractPluginTOML() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateGeneratedSpec(t *testing.T) {
	if _, err := validateGeneratedSpec("kubectl", validKubectlTOML); err != nil {
		t.Errorf("valid spec rejected: %v", err)
	}

	if _, err := validateGeneratedSpec("kubectl", ""); err == nil {
		t.Error("empty spec accepted")
	}

	if _, err := validateGeneratedSpec("kubectl", "not toml ["); err == nil {
		t.Error("broken TOML accepted")
	}

	// Spec that doesn't cover the requested tool.
	other := strings.ReplaceAll(validKubectlTOML, `commands = ["kubectl"]`, `commands = ["helm"]`)
	if _, err := validateGeneratedSpec("kubectl", other); err == nil {
		t.Error("spec without the tool in commands accepted")
	}

	// Disabled specs make no sense when generating.
	disabled := `[plugin]
name = "kubectl"
commands = ["kubectl"]
disabled = true
`
	if _, err := validateGeneratedSpec("kubectl", disabled); err == nil {
		t.Error("disabled spec accepted")
	}
}

func TestValidatePluginToolName(t *testing.T) {
	for _, ok := range []string{"kubectl", "docker-compose", "gh"} {
		if err := validatePluginToolName(ok); err != nil {
			t.Errorf("validatePluginToolName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "a/b", `a\b`} {
		if err := validatePluginToolName(bad); err == nil {
			t.Errorf("validatePluginToolName(%q) = nil, want error", bad)
		}
	}
}

func TestBuildPluginGenPrompt(t *testing.T) {
	prompt := buildPluginGenPrompt("kubectl", "kubectl controls Kubernetes", "also complete contexts")
	for _, want := range []string{
		"`kubectl`",
		"[[rules]]",
		"value_column",
		"kubectl controls Kubernetes",
		"also complete contexts",
		"read-only",
		"ONLY the TOML",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	noHelp := buildPluginGenPrompt("kubectl", "", "")
	if strings.Contains(noHelp, "Help output") {
		t.Error("prompt should omit help section when no help text captured")
	}
}

func newTestGenerator(t *testing.T, ask func(ctx context.Context, prompt string) (string, error), confirm bool) (*pluginGenerator, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	return &pluginGenerator{
		ask:      ask,
		toolHelp: func(ctx context.Context, tool string) string { return "kubectl help text" },
		confirm:  func(prompt string) bool { return confirm },
		specsDir: t.TempDir(),
		out:      out,
	}, out
}

func TestPluginGenerator_Run(t *testing.T) {
	gen, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		return "```toml\n" + validKubectlTOML + "```", nil
	}, true)

	path, err := gen.run(context.Background(), "kubectl", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if filepath.Base(path) != "kubectl.toml" {
		t.Errorf("path = %q, want kubectl.toml", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading spec: %v", err)
	}
	if strings.Contains(string(data), "```") {
		t.Error("written spec still contains markdown fences")
	}
	if _, err := validateGeneratedSpec("kubectl", string(data)); err != nil {
		t.Errorf("written spec invalid: %v", err)
	}
}

func TestPluginGenerator_RunDeclined(t *testing.T) {
	gen, out := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		return validKubectlTOML, nil
	}, false)

	path, err := gen.run(context.Background(), "kubectl", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if path != "" {
		t.Errorf("path = %q, want empty when declined", path)
	}
	if entries, _ := os.ReadDir(gen.specsDir); len(entries) != 0 {
		t.Error("no file should be written when declined")
	}
	if !strings.Contains(out.String(), "Discarded") {
		t.Error("expected Discarded message")
	}
}

func TestPluginGenerator_RetriesOnceWithValidationError(t *testing.T) {
	calls := 0
	gen, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		calls++
		if calls == 1 {
			return "this is not toml [", nil
		}
		// The retry prompt must carry the validation error and prior draft.
		if !strings.Contains(prompt, "failed validation") || !strings.Contains(prompt, "this is not toml") {
			return "", fmt.Errorf("retry prompt missing context: %q", prompt)
		}
		return validKubectlTOML, nil
	}, true)

	path, err := gen.run(context.Background(), "kubectl", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if calls != 2 {
		t.Errorf("agent calls = %d, want 2", calls)
	}
	if path == "" {
		t.Error("expected spec written after successful retry")
	}
}

func TestPluginGenerator_FailsAfterRetries(t *testing.T) {
	calls := 0
	gen, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		calls++
		return "still not toml [", nil
	}, true)

	_, err := gen.run(context.Background(), "kubectl", "")
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}
	if calls != pluginGenTries {
		t.Errorf("agent calls = %d, want %d", calls, pluginGenTries)
	}
	if !strings.Contains(err.Error(), "Draft:") {
		t.Errorf("error should include the draft for manual fixing: %v", err)
	}
}

func TestPluginGenerator_AgentError(t *testing.T) {
	gen, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		return "", errors.New("agent exploded")
	}, true)

	if _, err := gen.run(context.Background(), "kubectl", ""); err == nil || !strings.Contains(err.Error(), "agent exploded") {
		t.Fatalf("err = %v, want agent error surfaced", err)
	}
}

func TestCollectToolHelp(t *testing.T) {
	// `sh --help` is not universal, but `sh -c ...` in a fake tool is.
	dir := t.TempDir()
	tool := filepath.Join(dir, "fake-tool")
	script := "#!/bin/sh\nif [ \"$1\" = \"--help\" ]; then echo 'usage: fake-tool'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}

	out := collectToolHelp(context.Background(), tool)
	if !strings.Contains(out, "usage: fake-tool") {
		t.Errorf("collectToolHelp = %q, want usage output", out)
	}
}

func TestCollectToolHelp_FallsBackToHelpSubcommand(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "fake-tool")
	script := "#!/bin/sh\nif [ \"$1\" = \"help\" ]; then echo 'from help subcommand'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}

	out := collectToolHelp(context.Background(), tool)
	if !strings.Contains(out, "from help subcommand") {
		t.Errorf("collectToolHelp = %q, want fallback output", out)
	}
}
