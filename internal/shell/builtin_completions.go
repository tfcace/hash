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
		review:   reviewOnStdin,
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

// genAction is what the user chose at the review prompt.
type genAction int

const (
	genAccept genAction = iota
	genRevise
	genQuit
)

// genChoice is one review decision. message carries the free-text instruction
// for genRevise, and is empty when the user just wants the agent to try again.
type genChoice struct {
	action  genAction
	message string
}

// pluginGenerator runs the agent-assisted plugin generation flow. Its
// collaborators are injected for testing.
type pluginGenerator struct {
	ask      func(ctx context.Context, prompt string) (string, error)
	toolHelp func(ctx context.Context, tool string) string
	// review presents the draft and returns what the user wants to do next.
	// allowAccept is false when the current draft failed validation, so there
	// is nothing worth saving yet.
	review   func(allowAccept bool) genChoice
	specsDir string
	out      io.Writer
}

// run drives the generate-review-revise conversation and returns the written
// spec path, or "" if the user quit. The number of rounds is up to the user:
// each revision is a follow-up to the agent, and a spec that fails validation
// is just another round rather than a hard failure.
func (g *pluginGenerator) run(ctx context.Context, tool, hints string) (string, error) {
	fmt.Fprintf(g.out, "Inspecting `%s --help`...\n", tool)
	helpText := g.toolHelp(ctx, tool)
	if helpText == "" {
		fmt.Fprintf(g.out, "No help output captured; the agent will rely on its own knowledge of %s.\n", tool)
	}

	prompt := buildPluginGenPrompt(tool, helpText, hints)
	fmt.Fprintf(g.out, "Asking the agent to draft a completion plugin for %s...\n", tool)

	for {
		reply, err := g.ask(ctx, prompt)
		if err != nil {
			return "", fmt.Errorf("agent request failed: %w", err)
		}

		specTOML := extractPluginTOML(reply)
		spec, validErr := validateGeneratedSpec(tool, specTOML)
		if validErr == nil {
			g.printPreview(tool, specTOML, spec)
		} else {
			fmt.Fprintf(g.out, "\nThe agent's spec failed validation: %v\n\nDraft:\n%s\n", validErr, specTOML)
		}

		choice := g.review(validErr == nil)
		switch choice.action {
		case genQuit:
			fmt.Fprintln(g.out, "Discarded.")
			return "", nil
		case genAccept:
			if validErr != nil {
				continue // Nothing valid to save; ask again.
			}
			return g.save(tool, specTOML)
		}

		if validErr != nil {
			prompt = buildPluginRetryPrompt(specTOML, validErr, choice.message)
		} else {
			prompt = buildPluginRevisePrompt(specTOML, choice.message)
		}
		fmt.Fprintln(g.out, "Asking the agent to revise...")
	}
}

// save writes the accepted spec, replacing any existing file for the tool.
func (g *pluginGenerator) save(tool, specTOML string) (string, error) {
	if err := os.MkdirAll(g.specsDir, 0o750); err != nil {
		return "", fmt.Errorf("create %s: %w", g.specsDir, err)
	}
	path := filepath.Join(g.specsDir, tool+".toml")
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

// reviewOnStdin prompts for the next step. A bare word picks an action; a
// revision may carry its instruction inline ("r also complete namespaces") or
// be typed at the follow-up prompt. Anything unrecognized is treated as a
// revision instruction, since that is what free text almost always is.
func reviewOnStdin(allowAccept bool) genChoice {
	reader := bufio.NewReader(os.Stdin)
	for {
		if allowAccept {
			fmt.Print("\n[a]ccept  [r]evise <what to change>  [q]uit: ")
		} else {
			fmt.Print("\n[r]evise <what to change>  [q]uit: ")
		}

		input, err := reader.ReadString('\n')
		if err != nil {
			return genChoice{action: genQuit}
		}
		input = strings.TrimSpace(input)

		verb, rest, _ := strings.Cut(input, " ")
		rest = strings.TrimSpace(rest)

		switch strings.ToLower(verb) {
		case "":
			continue
		case "q", "quit":
			return genChoice{action: genQuit}
		case "a", "accept":
			if allowAccept {
				return genChoice{action: genAccept}
			}
			fmt.Println("Nothing valid to accept yet.")
			continue
		case "r", "revise":
			if rest == "" {
				fmt.Print("What should change? (blank to let the agent retry): ")
				line, err := reader.ReadString('\n')
				if err != nil {
					return genChoice{action: genQuit}
				}
				rest = strings.TrimSpace(line)
			}
			return genChoice{action: genRevise, message: rest}
		default:
			return genChoice{action: genRevise, message: input}
		}
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
timeout = "3s"                  # optional; bounds a background process, not the TAB.
                                # A source killed by its timeout caches nothing and
                                # fails the same way every time, so be generous.
cache_ttl = "30s"               # optional; expired results are still served while a
                                # refresh runs in the background, so prefer 10s-60s

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

func buildPluginRetryPrompt(previousTOML string, validationErr error, instruction string) string {
	var b strings.Builder
	b.WriteString("The plugin spec you produced failed validation.\n\nError: ")
	b.WriteString(validationErr.Error())
	b.WriteString("\n\nYour previous spec:\n")
	b.WriteString(previousTOML)
	if instruction != "" {
		b.WriteString("\n\nThe user also asks: ")
		b.WriteString(instruction)
	}
	b.WriteString("\n\nFix the problem and reply with ONLY the corrected TOML file content — no markdown fences, no commentary.")
	return b.String()
}

// buildPluginRevisePrompt asks for a change to a spec that already validates.
// The current spec is restated rather than relied on from conversation
// history, so revision works on transports that keep no history.
func buildPluginRevisePrompt(currentTOML, instruction string) string {
	var b strings.Builder
	b.WriteString("Revise this completion plugin spec.\n\nCurrent spec:\n")
	b.WriteString(currentTOML)
	b.WriteString("\n\nRequested change: ")
	b.WriteString(instruction)
	b.WriteString("\n\nKeep everything else intact. The same rules as before still apply: sources must be read-only, non-interactive, and fast, and you must not invent subcommands the tool does not have.")
	b.WriteString("\n\nReply with ONLY the full revised TOML file content — no markdown fences, no commentary.")
	return b.String()
}
