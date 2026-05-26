package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/shell"
	"github.com/tfcace/hash/internal/trace"
	"github.com/tfcace/hash/internal/version"
)

//nolint:gocyclo // main has flag parsing branches - inherently sequential
func main() {
	mode := DetectMode(os.Args)
	args := os.Args[1:]

	// Handle subcommands before flag parsing
	if len(args) > 0 && args[0] == "migrate" {
		os.Exit(runMigrate(args[1:]))
	}

	// Parse flags manually (order-independent)
	var command string
	var commandIdx = -1

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-l", "--login":
			mode.Login = true
		case "-c":
			mode.Command = true
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "hash: -c: option requires an argument\n")
				os.Exit(2)
			}
			command = args[i+1]
			commandIdx = i
			i++
		case "-v", "--version":
			printVersion()
			os.Exit(0)
		case "-h", "--help":
			printHelp()
			os.Exit(0)
		}
	}

	// -c implies non-interactive
	if mode.Command {
		mode.Interactive = false
	} else {
		mode.Interactive = IsInteractive()
	}

	// Set session markers
	if mode.Login {
		os.Setenv("HASH_LOGIN", "1")
	}
	if mode.Interactive {
		os.Setenv("HASH_INTERACTIVE", "1")
	}

	// Handle -c command
	if mode.Command {
		// Get positional args after the command (for $0, $1, etc.)
		var positionalArgs []string
		if commandIdx >= 0 && commandIdx+2 < len(args) {
			positionalArgs = args[commandIdx+2:]
		}
		os.Exit(runCommand(command, positionalArgs, mode))
	}

	if err := run(mode); err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`Usage: hash [OPTIONS] [-c COMMAND [ARGS...]]

Options:
  -l, --login     Start as a login shell
  -c COMMAND      Execute COMMAND and exit
  -v, --version   Show version
  -h, --help      Show this help

Subcommands:
  migrate         Import compatible bash/zsh settings

Interactive builtins:
  tips            Show common Hash shortcuts and AI syntax
  status          Show subsystem status
  history         Search and inspect command history
  copy            Copy recent command output
  issue           Draft a GitHub issue from shell context
  setup-zoxide    Configure zoxide integration

Environment:
  HASH_LOGIN=1        Set when running as login shell
  HASH_INTERACTIVE=1  Set when running interactively
  HASH_SHELL=1        Always set to identify Hash

Startup files:
  Login shell:       /etc/profile, ~/.profile, ~/.hash_profile
  Interactive shell: ~/.hashrc
  Login+Interactive: All of the above`)
}

func printVersion() {
	fmt.Printf("hash %s\n", version.String())
}

// runCommand executes a single command and returns its exit code.
func runCommand(command string, positionalArgs []string, mode ShellMode) int {
	exec := executor.New()

	// Set positional arguments for the command ($0, $1, $2, etc.)
	if len(positionalArgs) > 0 {
		exec.SetPositionalArgs(positionalArgs)
	}

	result, err := exec.Execute(context.Background(), command, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		return 1
	}
	return result.ExitCode
}

func run(mode ShellMode) error {
	// Initialize tracing (before anything else)
	if err := trace.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "hash: trace init failed: %v\n", err)
	}
	defer trace.Close()

	// Load configuration (warns but continues on parse errors)
	configDir := getConfigDir()
	cfg, err := config.Load(configDir)
	if err != nil {
		// Parse errors return defaults with error - warn but continue
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
	}

	// Create shell with mode
	sh, err := shell.NewWithMode(cfg, shell.Mode{
		Login:       mode.Login,
		Interactive: mode.Interactive,
	})
	if err != nil {
		return err
	}

	// Set up context with signal handling.
	// Only SIGTERM cancels the shell context (for graceful shutdown).
	// SIGINT (Ctrl+C) is handled locally by the shell components that need it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Run the shell
	return sh.Run(ctx)
}

func getConfigDir() string {
	if dir := os.Getenv("HASH_CONFIG_DIR"); dir != "" {
		return dir
	}

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "hash")
	}

	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "hash")
}
