package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/history"
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

// isBuiltin returns true if the command is a shell builtin.
func isBuiltin(cmd string) bool {
	switch cmd {
	case "cd", "exit", "quit", "history", "copy", "issue", "status", "tips", "setup-zoxide":
		return true
	// Source builtin with LangBash support
	case "source", ".":
		return true
	// No-op builtins for zsh compatibility
	case "bindkey", "setopt", "unsetopt", "autoload", "compdef", "zstyle", "zmodload", "zle", "compinit", "promptinit":
		return true
	default:
		return false
	}
}

// executeBuiltin runs a builtin command. Returns (handled, error).
func (s *Shell) executeBuiltin(ctx context.Context, line string) (bool, error) { //nolint:gocyclo // switch over builtin commands
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false, nil
	}

	cmd := parts[0]
	args := parts[1:]

	// Check if builtin is enabled
	if !isBuiltinEnabled(s.config, cmd) {
		return false, nil // Fall through to external command
	}

	switch cmd {
	case "cd":
		err := builtinCd(args)
		if err == nil && s.executor != nil {
			if cwd, cwdErr := os.Getwd(); cwdErr == nil {
				s.executor.SetExportedEnv("PWD", cwd)
			}
			// Sync the persistent runner's directory with the process
			s.executor.SyncRunnerDir()
		}
		return true, err
	case "exit", "quit":
		return true, errExit
	case "history":
		return true, s.builtinHistory(args)
	case "copy":
		return true, s.builtinCopy(args)
	case "issue":
		return true, s.builtinIssue(args)
	case "status":
		return true, s.builtinStatus()
	case "tips":
		return true, s.builtinTips(args)
	case "setup-zoxide":
		return true, s.builtinSetupZoxide(ctx, args)
	case "source", ".":
		return true, s.builtinSource(ctx, args)
	case "bindkey", "setopt", "unsetopt", "autoload", "compdef", "zstyle", "zmodload", "zle", "compinit", "promptinit":
		// No-op for zsh compatibility - silently succeed
		return true, nil
	default:
		return false, nil
	}
}

var errExit = fmt.Errorf("exit")

func builtinCd(args []string) error {
	var target string

	if len(args) == 0 {
		target = os.Getenv("HOME")
	} else {
		target = args[0]
	}

	// Expand tilde
	if strings.HasPrefix(target, "~") {
		home := os.Getenv("HOME")
		if home != "" {
			target = filepath.Join(home, target[1:])
		}
	}

	if err := os.Chdir(target); err != nil {
		return err
	}

	// Update PWD environment variable - required for tools like Starship
	// that read $PWD instead of calling getcwd()
	if newCwd, err := os.Getwd(); err == nil {
		os.Setenv("PWD", newCwd)
	}

	return nil
}

// builtinHistory handles the history command.
func (s *Shell) builtinHistory(args []string) error {
	if s.history == nil {
		return fmt.Errorf("history not available")
	}

	if len(args) == 0 {
		// Show recent history
		return s.showRecentHistory(20)
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "search":
		if len(subargs) == 0 {
			return fmt.Errorf("usage: history search <query>")
		}
		return s.searchHistory(strings.Join(subargs, " "))

	case "failed":
		return s.showFailedHistory()

	case "sudo":
		return s.showSudoHistory()

	case "asked":
		if len(subargs) == 0 {
			return s.showAgentHistory("")
		}
		return s.showAgentHistory(subargs[0])

	case "clear":
		return fmt.Errorf("history clear: not implemented (use sqlite3 directly)")

	default:
		// Treat as search
		return s.searchHistory(strings.Join(args, " "))
	}
}

func (s *Shell) showRecentHistory(n int) error {
	commands, err := s.history.GetRecent(n)
	if err != nil {
		return err
	}

	for i := range commands {
		num := len(commands) - i
		fmt.Printf("%5d  %s\n", num, commands[i].Command)
	}
	return nil
}

func (s *Shell) searchHistory(query string) error {
	results, err := s.history.Search(history.SearchOptions{
		Query: query,
		Limit: 20,
	})
	if err != nil {
		return err
	}

	for i := range results {
		fmt.Printf("  %s  \033[90m(%s)\033[0m\n", results[i].Command, results[i].Timestamp.Format("2006-01-02"))
	}
	return nil
}

func (s *Shell) showFailedHistory() error {
	results, err := s.history.Search(history.SearchOptions{
		OnlyFailed: true,
		Limit:      20,
	})
	if err != nil {
		return err
	}

	for i := range results {
		fmt.Printf("  \033[31mx%d\033[0m %s\n", results[i].ExitCode, results[i].Command)
	}
	return nil
}

func (s *Shell) showSudoHistory() error {
	results, err := s.history.Search(history.SearchOptions{
		OnlySudo: true,
		Limit:    20,
	})
	if err != nil {
		return err
	}

	for i := range results {
		fmt.Printf("  \033[33m#\033[0m %s  \033[90m(as %s)\033[0m\n", results[i].Command, results[i].SudoUser)
	}
	return nil
}

func (s *Shell) showAgentHistory(query string) error {
	interactions, err := s.history.GetAgentInteractions(query, 20)
	if err != nil {
		return err
	}

	for _, i := range interactions {
		status := "\033[32m+\033[0m"
		if !i.Accepted {
			status = "\033[31m-\033[0m"
		}
		fmt.Printf("  %s \033[36m??\033[0m %s\n", status, i.Prompt)
		fmt.Printf("    -> %s\n", i.Response)
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

	subcommand := args[0]

	// Parse optional index
	index := 0
	if len(args) >= 2 {
		var err error
		index, err = strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid index: %s", args[1])
		}
		// Convert from 1-indexed user input to 0-indexed internal
		if index > 0 {
			index--
		}
	}

	var content string

	switch subcommand {
	case "cmd":
		content = s.clipboard.GetCommand(index)
		if content == "" {
			return fmt.Errorf("no command at index %d", index)
		}

	case "out":
		content = s.clipboard.GetOutput(index)
		if content == "" {
			return fmt.Errorf("no output at index %d", index)
		}

	case "all":
		cmd, out := s.clipboard.GetBoth(index)
		if cmd == "" {
			return fmt.Errorf("no entry at index %d", index)
		}
		content = fmt.Sprintf("$ %s\n%s", cmd, out)

	default:
		return fmt.Errorf("unknown subcommand: %s (use cmd, out, or all)", subcommand)
	}

	// Copy to system clipboard
	if err := copyToSystemClipboard(content); err != nil {
		return fmt.Errorf("clipboard: %w", err)
	}

	fmt.Printf("Copied to clipboard (%d bytes)\n", len(content))
	return nil
}

// copyToSystemClipboard copies text to the system clipboard.
func copyToSystemClipboard(text string) error {
	if err := sysClipboard.Init(); err != nil {
		return err
	}
	sysClipboard.Write(sysClipboard.FmtText, []byte(text))
	return nil
}

// ClipboardBuffer is a type alias for easier external access.
type ClipboardBuffer = clipboard.Buffer

// builtinStatus shows the current system status.
func (s *Shell) builtinStatus() error {
	status := s.collectStatus()
	fmt.Print(status.Format())
	return nil
}

// builtinTips shows helpful tips about Hash features.
func (s *Shell) builtinTips(args []string) error {
	if len(args) > 0 && (args[0] == "off" || args[0] == "on") {
		fmt.Println("Startup tips are shown only once. Run 'tips' anytime to view them.")
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

	fmt.Println(headerStyle.Render("Hash Tips:"))
	fmt.Println()

	fmt.Println(headerStyle.Render("Navigation:"))
	fmt.Printf("  %s  %s\n", keyStyle.Render("Ctrl+R"), dimStyle.Render("Search command history"))
	fmt.Printf("  %s  %s\n", keyStyle.Render("Ctrl+P"), dimStyle.Render("Context picker for AI requests"))
	fmt.Printf("  %s %s\n", keyStyle.Render("Up/Down"), dimStyle.Render("Browse history"))
	fmt.Println()

	fmt.Println(headerStyle.Render("AI features:"))
	fmt.Printf("  %s      %s\n", keyStyle.Render("??"), dimStyle.Render("Full command generation"))
	fmt.Printf("  %s  %s\n", keyStyle.Render("cmd | ??"), dimStyle.Render("Pipe to AI for transformation"))
	fmt.Printf("  %s    %s\n", keyStyle.Render("cmd ??"), dimStyle.Render("Inline completion"))
	fmt.Println()

	fmt.Println(headerStyle.Render("Clipboard:"))
	fmt.Printf("  %s  %s\n", keyStyle.Render("Ctrl+Y"), dimStyle.Render("Copy last command"))
	fmt.Printf("  %s  %s\n", keyStyle.Render("Ctrl+O"), dimStyle.Render("Copy last output"))
	fmt.Println()

	fmt.Println(dimStyle.Render("Startup tips are shown once; run 'tips' anytime to view this list."))
	return nil
}

// collectStatus gathers the current status of all subsystems.
func (s *Shell) collectStatus() *SystemStatus {
	agentCfg := s.config.EffectiveAgent()
	status := &SystemStatus{
		Version: version.Version,
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

// builtinSource sources a shell script file using LangBash parsing.
// This ensures bash syntax like [[ ]] and == works correctly.
func (s *Shell) builtinSource(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("source: need filename")
	}

	path := args[0]

	// Expand tilde
	if strings.HasPrefix(path, "~") {
		home := os.Getenv("HOME")
		if home != "" {
			path = filepath.Join(home, path[1:])
		}
	}

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("source: %s: is a directory", path)
	}

	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}

	// Execute through the executor (which parses with LangBash)
	if s.executor != nil {
		_, err = s.executor.Execute(ctx, string(content), os.Stdout, os.Stderr)
		return err
	}

	return fmt.Errorf("source: executor not available")
}
