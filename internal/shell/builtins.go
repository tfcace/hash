package shell

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/agentupdate"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/modelpicker"
	"github.com/tfcace/hash/internal/version"
	sysClipboard "golang.design/x/clipboard"
)

// isBuiltinEnabled checks if a builtin is enabled in the config.
func isBuiltinEnabled(cfg *config.Config, name string) bool {
	if cfg == nil {
		return true
	}
	for _, disabled := range cfg.Shell.DisableBuiltins {
		if disabled == name {
			return false
		}
	}
	return true
}

// completableBuiltins are the builtins offered by tab completion in command
// position. zsh's setup builtins, no-ops the interpreter runs (see the
// executor's zshSetupBuiltins), are deliberately absent: they exist to
// swallow sourced zshrc lines, not to be typed. source is the interpreter's
// (a sourced file is the line: its jobs, status, arguments and exit), not
// one hash runs itself (isBuiltin).
var completableBuiltins = []string{
	"cd", "completions", "copy", "exit", "history", "issue", "model",
	"quit", "setup", "setup-zoxide", "source", "status", "tips",
}

// enabledCompletableBuiltins filters completableBuiltins by the config's
// disabled-builtins list.
func enabledCompletableBuiltins(cfg *config.Config) []string {
	names := make([]string, 0, len(completableBuiltins))
	for _, name := range completableBuiltins {
		if isBuiltinEnabled(cfg, name) {
			names = append(names, name)
		}
	}
	return names
}

// isBuiltin returns true if the command is a shell builtin.
func isBuiltin(cmd string) bool {
	switch cmd {
	case "exit", "quit", "history", "copy", "issue", "status", "tips", "setup", "setup-zoxide", "model", "completions":
		return true
	default:
		return false
	}
}

// executeBuiltin runs a builtin command. Returns (handled, error).
func (s *Shell) executeBuiltin(ctx context.Context, line string) (bool, error) { //nolint:gocyclo // switch over builtin commands
	fields := strings.Fields(line)
	if len(fields) == 0 || !isBuiltin(fields[0]) {
		return false, nil
	}

	// Only handle a builtin here when the line is a single simple command of
	// literal words. Pipelines, redirections, control operators, expansions
	// and assignments belong to the interpreter, which sees the same builtins
	// through registerExecutorBuiltins where they produce output.
	parts, ok := s.simpleCommandWords(line)
	if !ok {
		return false, nil
	}

	cmd := parts[0]
	args := parts[1:]

	// Check if builtin is enabled
	if !isBuiltinEnabled(s.config, cmd) {
		return false, nil // Fall through to external command
	}

	// Builtins run in-process, some of them waiting on the agent or npm:
	// Ctrl+C cancels the builtin, as it would a command.
	ctx, done := s.interrupts.interruptible(ctx)
	defer done()

	switch cmd {
	case "exit", "quit":
		return true, shellExit{status: s.exitArg(args)}
	case "copy":
		return true, s.builtinCopy(args)
	case "issue":
		return true, s.builtinIssue(args)
	case "status":
		return true, s.builtinStatus()
	case "tips":
		return true, s.builtinTips(args)
	case "setup":
		s.runOnboarding()
		return true, nil
	case "setup-zoxide":
		return true, s.builtinSetupZoxide(ctx, args)
	case "model":
		return true, s.builtinModel(ctx, args)
	case "completions":
		return true, s.builtinCompletions(ctx, args)
	default:
		return false, nil
	}
}

// exitArg is the status `exit [N]` ends the shell with: N, or the last
// line's status when there is none, as in bash. A status that is not a
// number ends the shell with 2, after saying so.
func (s *Shell) exitArg(args []string) int {
	if len(args) == 0 {
		return s.status.exitCode
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(s.errOut(), "hash: exit: %s: numeric argument required\n", args[0])
		return 2
	}
	return n & 0xff
}

// builtinHistory handles the history command.
func (s *Shell) builtinHistory(w io.Writer, args []string) error {
	if s.history == nil {
		return fmt.Errorf("history not available")
	}

	if len(args) == 0 {
		return s.listHistory(w, -1)
	}
	if n, err := strconv.Atoi(args[0]); err == nil && n >= 0 && len(args) == 1 {
		return s.listHistory(w, n) // history N: the last N, as in bash
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "search":
		if len(subargs) == 0 {
			return fmt.Errorf("usage: history search <query>")
		}
		return s.searchHistory(w, strings.Join(subargs, " "))

	case "failed":
		return s.showFailedHistory(w)

	case "sudo":
		return s.showSudoHistory(w)

	case "asked":
		if len(subargs) == 0 {
			return s.showAgentHistory(w, "")
		}
		return s.showAgentHistory(w, subargs[0])

	case "clear":
		return fmt.Errorf("history clear: not implemented (use sqlite3 directly)")

	default:
		// Treat as search
		return s.searchHistory(w, strings.Join(args, " "))
	}
}

// listHistory prints the last n recorded commands (every one when n < 0),
// oldest first, numbered from 1 in the whole list like bash's history
// builtin.
func (s *Shell) listHistory(w io.Writer, n int) error {
	commands, err := s.history.All()
	if err != nil {
		return err
	}

	from := 0
	if n >= 0 && n < len(commands) {
		from = len(commands) - n
	}
	for i := from; i < len(commands); i++ {
		fmt.Fprintf(w, "%5d  %s\n", i+1, commands[i].Command)
	}
	return nil
}

func (s *Shell) searchHistory(w io.Writer, query string) error {
	results, err := s.history.Search(history.SearchOptions{
		Query: query,
		Limit: 20,
	})
	if err != nil {
		return err
	}

	for i := range results {
		fmt.Fprintf(w, "  %s  \033[90m(%s)\033[0m\n", results[i].Command, results[i].Timestamp.Format("2006-01-02"))
	}
	return nil
}

func (s *Shell) showFailedHistory(w io.Writer) error {
	results, err := s.history.Search(history.SearchOptions{
		OnlyFailed: true,
		Limit:      20,
	})
	if err != nil {
		return err
	}

	for i := range results {
		fmt.Fprintf(w, "  \033[31mx%d\033[0m %s\n", results[i].ExitCode, results[i].Command)
	}
	return nil
}

func (s *Shell) showSudoHistory(w io.Writer) error {
	results, err := s.history.Search(history.SearchOptions{
		OnlySudo: true,
		Limit:    20,
	})
	if err != nil {
		return err
	}

	for i := range results {
		fmt.Fprintf(w, "  \033[33m#\033[0m %s  \033[90m(as %s)\033[0m\n", results[i].Command, results[i].SudoUser)
	}
	return nil
}

func (s *Shell) showAgentHistory(w io.Writer, query string) error {
	interactions, err := s.history.GetAgentInteractions(query, 20)
	if err != nil {
		return err
	}

	for _, i := range interactions {
		status := "\033[32m+\033[0m"
		if !i.Accepted {
			status = "\033[31m-\033[0m"
		}
		fmt.Fprintf(w, "  %s \033[36m??\033[0m %s\n", status, i.Prompt)
		fmt.Fprintf(w, "    -> %s\n", i.Response)
	}
	return nil
}

// builtinCopy handles the copy command.
// Usage:
//
//	copy cmd      - Copy last command
//	copy out      - Copy last output
//	copy cmd N    - Copy Nth-to-last command
//	copy all      - Copy command + output
func (s *Shell) builtinCopy(args []string) error {
	if s.clipboard == nil {
		return fmt.Errorf("clipboard not available")
	}

	if len(args) == 0 {
		return fmt.Errorf("usage: copy <cmd|out|all> [N]")
	}

	n, err := copyIndex(args[1:])
	if err != nil {
		return err
	}
	content, err := s.copyContent(args[0], n)
	if err != nil {
		return err
	}

	// Copy to system clipboard
	if err := copyToSystemClipboard(content); err != nil {
		return fmt.Errorf("clipboard: %w", err)
	}

	fmt.Fprintf(s.out(), "Copied to clipboard (%d bytes)\n", len(content))
	return nil
}

// copyIndex is the entry copy's arguments after the subcommand name,
// counted from 1 (the last line), as the user names it: 1 when they name
// none.
func copyIndex(args []string) (int, error) {
	if len(args) == 0 {
		return 1, nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, fmt.Errorf("invalid index: %s", args[0])
	}
	if n < 1 {
		return 0, fmt.Errorf("invalid index: %d (the last line is 1)", n)
	}
	return n, nil
}

// copyContent is what `copy sub n` puts on the clipboard.
func (s *Shell) copyContent(sub string, n int) (string, error) {
	index := n - 1 // the buffer counts from 0
	switch sub {
	case "cmd":
		if cmd := s.clipboard.GetCommand(index); cmd != "" {
			return cmd, nil
		}
		return "", fmt.Errorf("no command at index %d", n)
	case "out":
		if s.clipboard.GetCommand(index) == "" {
			return "", fmt.Errorf("no command at index %d", n)
		}
		switch s.clipboard.OutputState(index) {
		case clipboard.OutputCaptured:
			return s.clipboard.GetOutput(index), nil
		case clipboard.OutputUncaptured:
			return "", errWroteToTerminal(n)
		}
		return "", fmt.Errorf("%s printed nothing", copyEntryName(n))
	case "all":
		cmd, out := s.clipboard.GetBoth(index)
		if cmd == "" {
			return "", fmt.Errorf("no entry at index %d", n)
		}
		if s.clipboard.OutputState(index) == clipboard.OutputUncaptured {
			return "", errWroteToTerminal(n)
		}
		return fmt.Sprintf("$ %s\n%s", cmd, out), nil
	}
	return "", fmt.Errorf("unknown subcommand: %s (use cmd, out, or all)", sub)
}

// copyEntryName names copy's entry n (counted from 1) in a message.
func copyEntryName(n int) string {
	if n == 1 {
		return "the last command"
	}
	return fmt.Sprintf("command %d", n)
}

// errWroteToTerminal is why copy has no output for entry n: its command
// wrote straight to the terminal (a pipeline's tail), which hash never sees.
func errWroteToTerminal(n int) error {
	return fmt.Errorf("%s wrote straight to the terminal: hash kept none of its output", copyEntryName(n))
}

// copyToSystemClipboard copies text to the system clipboard.
func copyToSystemClipboard(text string) error {
	if err := sysClipboard.Init(); err != nil {
		return err
	}
	_, err := sysClipboard.Write(context.Background(), sysClipboard.FmtText, []byte(text))
	return err
}

// ClipboardBuffer is a type alias for easier external access.
type ClipboardBuffer = clipboard.Buffer

// builtinModel lists/selects the agent model. With no args it opens a TUI
// picker; `model <name>` selects directly; `model --list` prints the choices;
// `model update` installs the latest Claude adapter so the list is current.
// The selection persists for the duration of the shell session.
func (s *Shell) builtinModel(ctx context.Context, args []string) error {
	if s.agentHandler == nil {
		return fmt.Errorf("no agent configured")
	}

	// `model update`: refresh the adapter so the list below is current.
	if len(args) > 0 && args[0] == "update" {
		return s.runModelUpdate(ctx, s.out())
	}

	// Establish a session if needed so the agent reports its model options.
	if err := s.agentHandler.EnsureModelInfo(ctx); err != nil {
		return fmt.Errorf("agent unavailable: %w", err)
	}

	models := s.agentHandler.AvailableModels()
	if len(models) == 0 {
		fmt.Fprintln(s.out(), "this agent doesn't expose model selection")
		return nil
	}

	current := s.agentHandler.CurrentModel()

	// `model --list` / `model list`: print non-interactively.
	if len(args) > 0 && isListArg(args[0]) {
		printModelList(s.out(), models, current)
		return nil
	}

	// `model <name>`: select directly by value or display name.
	if len(args) > 0 {
		target := strings.Join(args, " ")
		value, ok := resolveModel(models, target)
		if !ok {
			return fmt.Errorf("unknown model %q; run `model --list` to see options", target)
		}
		return s.applyModel(ctx, models, value)
	}

	// No args: open the picker.
	value, ok, err := modelpicker.NewPickerUI(models, current).Run(ctx)
	if err != nil {
		return fmt.Errorf("model picker: %w", err)
	}
	if !ok {
		return nil // user canceled
	}
	return s.applyModel(ctx, models, value)
}

// applyModel sets the model and prints a confirmation using its display name.
func (s *Shell) applyModel(ctx context.Context, models []agent.ModelOption, value string) error {
	if err := s.agentHandler.SetModel(ctx, value); err != nil {
		return err
	}
	fmt.Fprintf(s.out(), "agent model set to %s\n", modelDisplayName(models, value))
	return nil
}

func isListArg(arg string) bool {
	switch arg {
	case "--list", "-l", "list":
		return true
	default:
		return false
	}
}

// resolveModel maps a user-typed name to a model value, matching the value or
// display name case-insensitively.
func resolveModel(models []agent.ModelOption, target string) (string, bool) {
	target = strings.TrimSpace(target)
	for _, m := range models {
		if strings.EqualFold(m.Value, target) || strings.EqualFold(m.Name, target) {
			return m.Value, true
		}
	}
	return "", false
}

// modelDisplayName returns the display name for a model value, or the value.
func modelDisplayName(models []agent.ModelOption, value string) string {
	for _, m := range models {
		if m.Value == value {
			if m.Name != "" {
				return m.Name
			}
			return m.Value
		}
	}
	return value
}

func printModelList(w io.Writer, models []agent.ModelOption, current string) {
	for _, m := range models {
		marker := "  "
		if m.Value == current || m.Name == current {
			marker = "* "
		}
		name := m.Name
		if name == "" {
			name = m.Value
		}
		if m.Description != "" {
			fmt.Fprintf(w, "%s%s  (%s)\n", marker, name, m.Description)
		} else {
			fmt.Fprintf(w, "%s%s\n", marker, name)
		}
	}
}

// builtinStatus shows the current system status.
func (s *Shell) builtinStatus() error {
	status := s.collectStatus()
	fmt.Fprint(s.out(), status.Format())
	return nil
}

// builtinTips shows helpful tips about Hash features.
func (s *Shell) builtinTips(args []string) error {
	if len(args) > 0 && (args[0] == "off" || args[0] == "on") {
		fmt.Fprintln(s.out(), "Startup tips are shown only once. Run 'tips' anytime to view them.")
		return nil
	}

	// Use colors from starship palette
	primary := s.colorPalette.Primary
	dim := s.colorPalette.Dim

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(primary)).
		Bold(true)

	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(primary))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(dim))

	fmt.Fprintln(s.out(), headerStyle.Render("Hash Tips:"))
	fmt.Fprintln(s.out())

	fmt.Fprintln(s.out(), headerStyle.Render("Navigation:"))
	fmt.Fprintf(s.out(), "  %s  %s\n", keyStyle.Render("Ctrl+R"), dimStyle.Render("Search command history"))
	fmt.Fprintf(s.out(), "  %s  %s\n", keyStyle.Render("Ctrl+P"), dimStyle.Render("Context picker for AI requests"))
	fmt.Fprintf(s.out(), "  %s %s\n", keyStyle.Render("Up/Down"), dimStyle.Render("Browse history"))
	fmt.Fprintln(s.out())

	fmt.Fprintln(s.out(), headerStyle.Render("AI features:"))
	fmt.Fprintf(s.out(), "  %s      %s\n", keyStyle.Render("??"), dimStyle.Render("Full command generation"))
	fmt.Fprintf(s.out(), "  %s  %s\n", keyStyle.Render("cmd | ??"), dimStyle.Render("Pipe to AI for transformation"))
	fmt.Fprintf(s.out(), "  %s    %s\n", keyStyle.Render("cmd ??"), dimStyle.Render("Inline completion"))
	fmt.Fprintf(s.out(), "  %s  %s\n", keyStyle.Render("completions generate <tool>"), dimStyle.Render("AI-generated tab completion plugin"))
	fmt.Fprintln(s.out())

	fmt.Fprintln(s.out(), headerStyle.Render("Clipboard:"))
	fmt.Fprintf(s.out(), "  %s  %s\n", keyStyle.Render("copy cmd"), dimStyle.Render("Copy the last command"))
	fmt.Fprintf(s.out(), "  %s  %s\n", keyStyle.Render("copy out"), dimStyle.Render("Copy the last output"))
	fmt.Fprintf(s.out(), "  %s  %s\n", keyStyle.Render("Ctrl+Y / Ctrl+O in Ctrl+R"), dimStyle.Render("Copy the selected command / its output"))
	fmt.Fprintln(s.out())

	fmt.Fprintln(s.out(), dimStyle.Render("Startup tips are shown once; run 'tips' anytime to view this list."))
	return nil
}

// collectStatus gathers the current status of all subsystems.
func (s *Shell) collectStatus() *SystemStatus {
	agentCfg := s.config.EffectiveAgent()
	status := &SystemStatus{
		Version: version.Version,
	}

	// Config status
	status.ConfigPath = filepath.Join(getConfigDir(), "config.toml")
	if s.config != nil && s.config.LoadIssue != nil {
		issue := s.config.LoadIssue
		status.ConfigPath = issue.Path
		switch issue.Kind {
		case config.LoadReadFailed:
			status.ConfigErr = "could not be read, all settings on defaults (" + issue.Detail + ")"
		case config.LoadPartial:
			status.ConfigErr = "defaults used for [" + strings.Join(issue.BadSections, "], [") + "] (" + issue.Detail + ")"
		default:
			status.ConfigErr = "parse error, all settings on defaults (" + issue.Detail + ")"
		}
	} else {
		status.ConfigOK = true
	}

	// Prompt status
	status.PromptMode = s.config.Prompt.Mode
	status.PromptOK = s.prompt != nil
	if !status.PromptOK {
		status.PromptErr = "not available"
	}

	// History status
	switch {
	case s.config != nil && !s.config.History.Enabled:
		status.HistoryErr = "disabled in config"
	case s.history != nil:
		status.HistoryOK = true
		status.HistoryPath = s.historyPath
		if count, err := s.history.Count(); err == nil {
			status.HistoryCount = count
		}
	default:
		status.HistoryErr = "not initialized"
	}

	// Learning status
	if s.learning != nil {
		status.LearningOK = true
		if count, err := s.learning.PatternCount(); err == nil {
			status.PatternCount = count
		}
	} else {
		status.LearningErr = "not initialized"
	}

	// Agent status
	status.AgentName = agentCfg.Default
	if status.AgentName == "" {
		status.AgentName = agentCfg.Command
	}
	status.AgentOK = s.agentHandler != nil
	status.AgentVersion, status.AgentLatest = s.adapterVersions(agentCfg)

	// PTY status - check if executor supports PTY
	status.PTYOK = true // PTY is always available on unix systems
	if s.executor == nil {
		status.PTYOK = false
		status.PTYErr = "executor not initialized"
	}

	// Clipboard status
	if err := sysClipboard.Init(); err != nil {
		status.ClipboardOK = false
		status.ClipboardErr = err.Error()
	} else {
		status.ClipboardOK = true
	}

	return status
}

// adapterVersions reports the installed version of the npm Claude adapter
// behind the agent command and, when the daily check has recorded a newer
// one, that version too. Both are "" for any other agent.
func (s *Shell) adapterVersions(agentCfg config.AgentConfig) (installed, latest string) {
	if agentCfg.Transport == "http" {
		return "", ""
	}
	inst, ok := agentupdate.Detect(agentCfg.Command, exec.LookPath)
	if !ok {
		return "", ""
	}
	if known := agentupdate.LoadState(s.updateStatePath()).Latest; agentupdate.Compare(known, inst.Version) > 0 {
		latest = known
	}
	return inst.Version, latest
}

// registerExecutorBuiltins exposes the builtins that produce output inside
// the interpreter, so they compose with pipelines and redirections.
func (s *Shell) registerExecutorBuiltins() {
	if s.executor == nil {
		return
	}
	if isBuiltinEnabled(s.config, "history") {
		s.executor.RegisterBuiltin("history", func(_ context.Context, args []string, stdout, _ io.Writer) error {
			return s.builtinHistory(stdout, args)
		})
	}
}

// simpleCommandWords splits line into words when it is a single simple
// command the REPL can handle literally. Without an executor it falls back to
// whitespace splitting.
func (s *Shell) simpleCommandWords(line string) ([]string, bool) {
	if s.executor == nil {
		fields := strings.Fields(line)
		return fields, len(fields) > 0
	}
	return s.executor.SimpleCommandWords(line)
}
