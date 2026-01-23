package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/history"
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
	case "cd", "exit", "quit", "history", "copy", "issue":
		return true
	default:
		return false
	}
}

// isBuiltinWithConfig returns true if the command is a shell builtin and is enabled.
func isBuiltinWithConfig(cfg *config.Config, cmd string) bool {
	if !isBuiltin(cmd) {
		return false
	}
	return isBuiltinEnabled(cfg, cmd)
}

// executeBuiltin runs a builtin command. Returns (handled, error).
func (s *Shell) executeBuiltin(line string) (bool, error) {
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

	for i, cmd := range commands {
		num := len(commands) - i
		fmt.Printf("%5d  %s\n", num, cmd.Command)
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

	for _, cmd := range results {
		fmt.Printf("  %s  \033[90m(%s)\033[0m\n", cmd.Command, cmd.Timestamp.Format("2006-01-02"))
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

	for _, cmd := range results {
		fmt.Printf("  \033[31mx%d\033[0m %s\n", cmd.ExitCode, cmd.Command)
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

	for _, cmd := range results {
		fmt.Printf("  \033[33m#\033[0m %s  \033[90m(as %s)\033[0m\n", cmd.Command, cmd.SudoUser)
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
