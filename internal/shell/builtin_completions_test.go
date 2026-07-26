package shell

import (
	"bytes"
	"context"
	"errors"
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

// scriptedReview replays a fixed sequence of review decisions and records the
// allowAccept flag it was shown each time. Running past the end is a bug in
// the test or an unterminated loop in the generator.
type scriptedReview struct {
	t       *testing.T
	choices []genChoice
	calls   int
	offered []bool
}

func (r *scriptedReview) review(allowAccept bool) genChoice {
	r.t.Helper()
	if r.calls >= len(r.choices) {
		r.t.Fatalf("review called %d times, script only has %d decisions", r.calls+1, len(r.choices))
	}
	r.offered = append(r.offered, allowAccept)
	choice := r.choices[r.calls]
	r.calls++
	return choice
}

func newTestGenerator(t *testing.T, ask func(ctx context.Context, prompt string) (string, error), choices ...genChoice) (*pluginGenerator, *bytes.Buffer, *scriptedReview) {
	t.Helper()
	out := &bytes.Buffer{}
	rev := &scriptedReview{t: t, choices: choices}
	return &pluginGenerator{
		ask:      ask,
		toolHelp: func(ctx context.Context, tool string) string { return "kubectl help text" },
		review:   rev.review,
		specsDir: t.TempDir(),
		out:      out,
	}, out, rev
}

func accept() genChoice         { return genChoice{action: genAccept} }
func quit() genChoice           { return genChoice{action: genQuit} }
func revise(m string) genChoice { return genChoice{action: genRevise, message: m} }

func TestPluginGenerator_Run(t *testing.T) {
	gen, _, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		return "```toml\n" + validKubectlTOML + "```", nil
	}, accept())

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

// When a plugin file for the tool already exists, the user must be told that
// accepting replaces it, and it must survive untouched if they quit.
func TestPluginGenerator_WarnsBeforeReplacingExistingPlugin(t *testing.T) {
	gen, out, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		return validKubectlTOML, nil
	}, quit())
	existing := filepath.Join(gen.specsDir, "kubectl.toml")
	if err := os.WriteFile(existing, []byte("# hand-tuned\n"), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}

	if _, err := gen.run(context.Background(), "kubectl", ""); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "replace") || !strings.Contains(out.String(), existing) {
		t.Errorf("output %q should warn that accepting replaces %s", out.String(), existing)
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "# hand-tuned\n" {
		t.Errorf("existing plugin changed on quit: %q, %v", data, err)
	}
}

// No existing file, no warning.
func TestPluginGenerator_NoReplaceWarningForNewPlugin(t *testing.T) {
	gen, out, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		return validKubectlTOML, nil
	}, accept())

	if _, err := gen.run(context.Background(), "kubectl", ""); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "replace the existing") {
		t.Errorf("output %q warns about replacing a file that does not exist", out.String())
	}
}

func TestPluginGenerator_QuitWritesNothing(t *testing.T) {
	gen, out, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		return validKubectlTOML, nil
	}, quit())

	path, err := gen.run(context.Background(), "kubectl", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if path != "" {
		t.Errorf("path = %q, want empty when the user quits", path)
	}
	if entries, _ := os.ReadDir(gen.specsDir); len(entries) != 0 {
		t.Error("no file should be written when the user quits")
	}
	if !strings.Contains(out.String(), "Discarded") {
		t.Error("expected Discarded message")
	}
}

// A revision sends the user's instruction plus the current spec, so the flow
// survives transports that keep no conversation history.
func TestPluginGenerator_ReviseThenAccept(t *testing.T) {
	const revisedTOML = `[plugin]
name = "kubectl"
commands = ["kubectl"]

[[rules]]
subcommands = ["delete pod", "logs", "config use-context"]
[rules.source]
exec = ["kubectl", "get", "pods", "-o", "name"]
`
	var prompts []string
	gen, _, rev := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		prompts = append(prompts, prompt)
		if len(prompts) == 1 {
			return validKubectlTOML, nil
		}
		return revisedTOML, nil
	}, revise("also complete contexts"), accept())

	path, err := gen.run(context.Background(), "kubectl", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("agent calls = %d, want 2", len(prompts))
	}
	if !strings.Contains(prompts[1], "also complete contexts") {
		t.Errorf("revision prompt missing the user's instruction: %q", prompts[1])
	}
	if !strings.Contains(prompts[1], "delete pod") {
		t.Errorf("revision prompt missing the current spec: %q", prompts[1])
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "config use-context") {
		t.Error("written spec is not the revised one")
	}
	for i, offered := range rev.offered {
		if !offered {
			t.Errorf("review %d: accept should be offered for a valid spec", i)
		}
	}
}

// The user decides how deep to go; nothing caps the number of rounds.
func TestPluginGenerator_RevisionsAreUncapped(t *testing.T) {
	calls := 0
	gen, _, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		calls++
		return validKubectlTOML, nil
	}, revise("one"), revise("two"), revise("three"), revise("four"), accept())

	if _, err := gen.run(context.Background(), "kubectl", ""); err != nil {
		t.Fatalf("run: %v", err)
	}
	if calls != 5 {
		t.Errorf("agent calls = %d, want 5 (4 revisions + initial)", calls)
	}
}

// An invalid spec is a turn in the loop, not a hard failure: the user is shown
// the error and can steer the fix. Accept must not be offered.
func TestPluginGenerator_ValidationFailureStaysInLoop(t *testing.T) {
	var prompts []string
	gen, out, rev := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		prompts = append(prompts, prompt)
		if len(prompts) == 1 {
			return "this is not toml [", nil
		}
		return validKubectlTOML, nil
	}, revise(""), accept())

	path, err := gen.run(context.Background(), "kubectl", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if path == "" {
		t.Fatal("expected a spec written after the user steered a fix")
	}
	if !strings.Contains(prompts[1], "this is not toml") {
		t.Errorf("fix prompt missing the rejected draft: %q", prompts[1])
	}
	if !strings.Contains(prompts[1], "failed validation") {
		t.Errorf("fix prompt missing the validation error: %q", prompts[1])
	}
	if len(rev.offered) < 1 || rev.offered[0] {
		t.Error("accept must not be offered while the draft is invalid")
	}
	if !strings.Contains(out.String(), "failed validation") {
		t.Error("the validation error should be shown to the user")
	}
}

// Accepting ends the conversation; no further agent calls or prompts.
func TestPluginGenerator_AcceptExitsLoop(t *testing.T) {
	calls := 0
	gen, _, rev := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		calls++
		return validKubectlTOML, nil
	}, accept())

	if _, err := gen.run(context.Background(), "kubectl", ""); err != nil {
		t.Fatalf("run: %v", err)
	}
	if calls != 1 {
		t.Errorf("agent calls = %d, want 1", calls)
	}
	if rev.calls != 1 {
		t.Errorf("review prompts = %d, want 1", rev.calls)
	}
}

func TestPluginGenerator_AgentError(t *testing.T) {
	gen, _, _ := newTestGenerator(t, func(ctx context.Context, prompt string) (string, error) {
		return "", errors.New("agent exploded")
	})

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

// Output is bounded while the command runs, not after: a tool that floods
// stdout must not balloon memory before the cap is applied.
func TestCollectToolHelp_CapsOutputWhileStreaming(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "fake-tool")
	script := "#!/bin/sh\nyes usage-line | head -c 1000000\nexit 0\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}

	out := collectToolHelp(context.Background(), tool)
	if len(out) == 0 || len(out) > toolHelpMaxBytes {
		t.Errorf("len(out) = %d, want capped at %d", len(out), toolHelpMaxBytes)
	}
}

func TestLimitedBuffer_CapsWrites(t *testing.T) {
	b := &limitedBuffer{max: 10}
	n, err := b.Write([]byte("0123456789abcdef"))
	if err != nil || n != 16 {
		t.Fatalf("Write = (%d, %v), want (16, nil): a full buffer must keep accepting", n, err)
	}
	if got := b.String(); got != "0123456789" {
		t.Errorf("String() = %q, want first 10 bytes", got)
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
