package shell

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/completion"
)

const (
	toolHelpTimeout  = 3 * time.Second
	toolHelpMaxBytes = 8 * 1024
	pluginGenTries   = 2
)

// builtinCompletions manages completion plugins:
//
//	completions list              - show registered plugin handlers
//	completions reload            - reload user specs from disk
//	completions generate <tool>   - agent-assisted plugin generation
func (s *Shell) builtinCompletions(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return s.completionsHelp()
	}

	switch args[0] {
	case "--help", "-h", "help":
		return s.completionsHelp()
	case "list":
		return s.completionsList()
	case "reload":
		return s.completionsReload()
	case "generate":
		return s.completionsGenerate(ctx, args[1:])
	default:
		return fmt.Errorf("completions: unknown subcommand %q (use list, reload, or generate)", args[0])
	}
}

func (s *Shell) completionsHelp() error {
	fmt.Println(`Usage: completions <subcommand>

Manage tab-completion plugins (see docs/completion-plugins.md).

Subcommands:
  list                       Show registered plugin handlers
  reload                     Reload user plugins from ` + completionsSpecsDirDisplay() + `
  generate <tool> [hints]    Ask the agent to write a plugin for <tool>

Examples:
  completions generate kubectl
  completions generate terraform "complete workspace names too"
  completions list`)
	return nil
}

func completionsSpecsDir() string {
	return filepath.Join(getConfigDir(), "completions")
}

func completionsSpecsDirDisplay() string {
	dir := completionsSpecsDir()
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(dir, home) {
		return "~" + dir[len(home):]
	}
	return dir
}

func (s *Shell) completionsList() error {
	if s.pluginCompleter == nil {
		fmt.Println("Completion plugins are disabled (completions.plugins_enabled = false).")
		return nil
	}

	infos := s.pluginCompleter.Plugins()
	if len(infos) == 0 {
		fmt.Println("No completion plugins registered.")
		return nil
	}

	fmt.Println("Completion plugins:")
	for _, info := range infos {
		origin := "user"
		if info.Builtin {
			origin = "built-in"
		}
		rules := "rule"
		if info.Rules != 1 {
			rules = "rules"
		}
		fmt.Printf("  %-14s %s (%s, %d %s)\n", info.Command, info.SpecName, origin, info.Rules, rules)
	}
	fmt.Printf("\nUser plugins live in %s\n", completionsSpecsDirDisplay())
	return nil
}

func (s *Shell) completionsReload() error {
	if err := s.reloadCompletionPlugins(); err != nil {
		return err
	}
	fmt.Println("Completion plugins reloaded.")
	return nil
}

// reloadCompletionPlugins re-reads user specs from disk into the live
// completer so new plugin files take effect without a restart.
func (s *Shell) reloadCompletionPlugins() error {
	if s.pluginCompleter == nil {
		return fmt.Errorf("completion plugins are disabled (completions.plugins_enabled = false)")
	}
	specs, errs := completion.LoadPluginSpecs(completionsSpecsDir())
	for _, err := range errs {
		fmt.Fprintf(os.Stderr, "hash: warning: completion plugin: %v\n", err)
	}
	s.pluginCompleter.SetUserSpecs(specs)
	return nil
}

func (s *Shell) completionsGenerate(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: completions generate <tool> [extra instructions]")
	}
	tool := args[0]
	hints := strings.Join(args[1:], " ")

	if err := validatePluginToolName(tool); err != nil {
		return err
	}
	if s.pluginCompleter == nil {
		return fmt.Errorf("completion plugins are disabled (completions.plugins_enabled = false)")
	}
	if s.agentHandler == nil {
		return fmt.Errorf("no agent configured; `completions generate` needs the ?? agent")
	}
	if _, err := exec.LookPath(tool); err != nil {
		return fmt.Errorf("%q not found in PATH; install it first so its --help output can be inspected", tool)
	}

	gen := &pluginGenerator{
		ask:      s.agentHandler.AskText,
		toolHelp: collectToolHelp,
		confirm:  confirmOnStdin,
		specsDir: completionsSpecsDir(),
		out:      os.Stdout,
	}

	path, err := gen.run(ctx, tool, hints)
	if err != nil || path == "" {
		return err
	}

	if err := s.reloadCompletionPlugins(); err != nil {
		return err
	}
	fmt.Printf("\nPlugin saved to %s and active now — try `%s <TAB>`.\n", path, tool)
	fmt.Println("Edit the file to tweak it, or `completions reload` after manual changes.")
	return nil
}

func validatePluginToolName(tool string) error {
	if tool == "" || strings.HasPrefix(tool, "-") || strings.ContainsAny(tool, " \t/\\") {
		return fmt.Errorf("invalid tool name %q", tool)
	}
	return nil
}

// pluginGenerator runs the agent-assisted plugin generation flow. Its
// collaborators are injected for testing.
type pluginGenerator struct {
	ask      func(ctx context.Context, prompt string) (string, error)
	toolHelp func(ctx context.Context, tool string) string
	confirm  func(prompt string) bool
	specsDir string
	out      io.Writer
}

// run drives generation and returns the written spec path, or "" if the user
// declined. Validation failures are retried once with the error fed back to
// the agent.
func (g *pluginGenerator) run(ctx context.Context, tool, hints string) (string, error) {
	fmt.Fprintf(g.out, "Inspecting `%s --help`...\n", tool)
	helpText := g.toolHelp(ctx, tool)
	if helpText == "" {
		fmt.Fprintf(g.out, "No help output captured; the agent will rely on its own knowledge of %s.\n", tool)
	}

	prompt := buildPluginGenPrompt(tool, helpText, hints)
	var specTOML string
	var spec *completion.PluginSpec

	for attempt := 1; ; attempt++ {
		fmt.Fprintf(g.out, "Asking the agent to draft a completion plugin for %s...\n", tool)
		reply, err := g.ask(ctx, prompt)
		if err != nil {
			return "", fmt.Errorf("agent request failed: %w", err)
		}

		specTOML = extractPluginTOML(reply)
		spec, err = validateGeneratedSpec(tool, specTOML)
		if err == nil {
			break
		}
		if attempt >= pluginGenTries {
			return "", fmt.Errorf("the agent's spec failed validation: %w\n\nDraft:\n%s", err, specTOML)
		}
		fmt.Fprintf(g.out, "Draft failed validation (%v); asking the agent to fix it...\n", err)
		prompt = buildPluginRetryPrompt(specTOML, err)
	}

	g.printPreview(tool, specTOML, spec)

	path := filepath.Join(g.specsDir, tool+".toml")
	confirmMsg := fmt.Sprintf("Save to %s? [y/N] ", path)
	if _, err := os.Stat(path); err == nil {
		confirmMsg = fmt.Sprintf("Overwrite existing %s? [y/N] ", path)
	}
	if !g.confirm(confirmMsg) {
		fmt.Fprintln(g.out, "Discarded.")
		return "", nil
	}

	if err := os.MkdirAll(g.specsDir, 0o750); err != nil {
		return "", fmt.Errorf("create %s: %w", g.specsDir, err)
	}
	if err := os.WriteFile(path, []byte(specTOML), 0o644); err != nil { //nolint:gosec // config file, not a secret
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

func (g *pluginGenerator) printPreview(tool, specTOML string, spec *completion.PluginSpec) {
	fmt.Fprintf(g.out, "\nGenerated plugin for %s:\n\n", tool)
	for _, line := range strings.Split(strings.TrimRight(specTOML, "\n"), "\n") {
		fmt.Fprintf(g.out, "  %s\n", line)
	}
	var covered []string
	for _, rule := range spec.Rules {
		covered = append(covered, rule.Subcommands...)
	}
	if len(covered) > 0 {
		fmt.Fprintf(g.out, "\n%d rules covering: %s\n", len(spec.Rules), strings.Join(covered, ", "))
	}
}

// validateGeneratedSpec parses the TOML and applies extra safety checks that
// only make sense for agent-generated specs.
func validateGeneratedSpec(tool, specTOML string) (*completion.PluginSpec, error) {
	if strings.TrimSpace(specTOML) == "" {
		return nil, fmt.Errorf("the reply contained no TOML")
	}
	spec, err := completion.ParsePluginSpec([]byte(specTOML))
	if err != nil {
		return nil, err
	}
	if spec.Plugin.Disabled {
		return nil, fmt.Errorf("generated spec must not set disabled = true")
	}
	for _, cmd := range spec.Plugin.Commands {
		if cmd == tool {
			return spec, nil
		}
	}
	return nil, fmt.Errorf("plugin.commands %v must include %q", spec.Plugin.Commands, tool)
}

// extractPluginTOML strips markdown code fences when the agent adds them
// despite the instructions.
func extractPluginTOML(reply string) string {
	reply = strings.TrimSpace(reply)
	if !strings.Contains(reply, "```") {
		return reply
	}

	lines := strings.Split(reply, "\n")
	var block []string
	inFence := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				break // closing fence of the first block
			}
			inFence = true
			continue
		}
		if inFence {
			block = append(block, line)
		}
	}
	return strings.TrimSpace(strings.Join(block, "\n"))
}

// collectToolHelp captures the tool's help output, trying the common help
// invocations in order.
func collectToolHelp(ctx context.Context, tool string) string {
	for _, args := range [][]string{{"--help"}, {"help"}, {"-h"}} {
		out := runToolHelpCommand(ctx, tool, args)
		if len(out) > 0 {
			return out
		}
	}
	return ""
}

func runToolHelpCommand(ctx context.Context, tool string, args []string) string {
	helpCtx, cancel := context.WithTimeout(ctx, toolHelpTimeout)
	defer cancel()

	cmd := exec.CommandContext(helpCtx, tool, args...) //nolint:gosec // tool name is validated and PATH-resolved
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf // Many tools print help to stderr.
	cmd.Stdin = nil
	_ = cmd.Run() // Non-zero exit is fine as long as we captured output.

	out := strings.TrimSpace(buf.String())
	if len(out) > toolHelpMaxBytes {
		out = out[:toolHelpMaxBytes]
	}
	return out
}

func confirmOnStdin(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.TrimSpace(strings.ToLower(input)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

const pluginSpecReference = `A plugin is a TOML file with this structure:

[plugin]
name = "<tool>"                 # required identifier
description = "..."             # optional
commands = ["<tool>"]           # required: command names this plugin completes

[[rules]]                       # one or more; first matching rule wins
subcommands = ["rm", "container rm"]  # subcommand paths; empty list = any arguments
max_args = 1                    # optional: stop completing after N positionals (0 = unlimited)
[rules.source]
exec = ["cmd", "arg"]           # required argv, run without a shell
delimiter = "\t"                # optional column separator (default: whitespace)
value_column = 1                # 1-based column inserted into the command line
description_column = 2          # optional 1-based column shown next to the value
timeout = "500ms"               # optional
cache_ttl = "2s"                # optional; raise for slow/stable sources

Example (systemd units):

[plugin]
name = "systemctl"
commands = ["systemctl"]

[[rules]]
subcommands = ["start", "stop", "restart", "status", "enable", "disable"]
[rules.source]
exec = ["systemctl", "list-units", "--all", "--plain", "--no-legend", "--no-pager"]
value_column = 1
description_column = 4
cache_ttl = "10s"`

func buildPluginGenPrompt(tool, helpText, hints string) string {
	var b strings.Builder
	b.WriteString("Write a tab-completion plugin for the hash shell that completes arguments for the `")
	b.WriteString(tool)
	b.WriteString("` command.\n\n")
	b.WriteString(pluginSpecReference)
	b.WriteString("\n\nRequirements:\n")
	b.WriteString("- source exec commands MUST be read-only, non-interactive, and fast; they run on every TAB press\n")
	b.WriteString("- prefer machine-readable output flags (--format, -o name, --porcelain, --no-legend) with a tab delimiter, and include a useful description_column when possible\n")
	b.WriteString("- cover the subcommands where completing a resource name helps most (remove/start/stop/inspect-style commands); include long-form paths like \"container rm\" when the tool has them\n")
	b.WriteString("- use max_args = 1 for subcommands where only the first positional is a resource name\n")
	b.WriteString("- only include rules the help output below supports; do not invent subcommands or flags\n")
	if helpText != "" {
		b.WriteString("\nHelp output of `")
		b.WriteString(tool)
		b.WriteString(" --help`:\n")
		b.WriteString(helpText)
		b.WriteString("\n")
	}
	if hints != "" {
		b.WriteString("\nExtra instructions from the user: ")
		b.WriteString(hints)
		b.WriteString("\n")
	}
	b.WriteString("\nReply with ONLY the TOML file content — no markdown fences, no commentary.")
	return b.String()
}

func buildPluginRetryPrompt(previousTOML string, validationErr error) string {
	var b strings.Builder
	b.WriteString("The plugin spec you produced failed validation.\n\nError: ")
	b.WriteString(validationErr.Error())
	b.WriteString("\n\nYour previous spec:\n")
	b.WriteString(previousTOML)
	b.WriteString("\n\nFix the problem and reply with ONLY the corrected TOML file content — no markdown fences, no commentary.")
	return b.String()
}
