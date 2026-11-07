package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/shell"
	"github.com/tfcace/hash/internal/trace"
	"github.com/tfcace/hash/internal/version"
)

func main() {
	mode, err := parseArgs(os.Args, stdinIsTerminal())
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		os.Exit(2)
	}

	switch m := mode.(type) {
	case migrateMode:
		os.Exit(runMigrate(m.args))
	case versionMode:
		printVersion()
		os.Exit(0)
	case helpMode:
		printHelp()
		os.Exit(0)
	case scriptMode:
		setMarker("HASH_LOGIN", m.login)
		setMarker("HASH_INTERACTIVE", m.rc)
		runScript(m)
	case interactiveMode:
		setMarker("HASH_LOGIN", m.login)
		setMarker("HASH_INTERACTIVE", true)
		runInteractive(shell.Mode{Login: m.login, Interactive: true})
	}
}

// setMarker sets the environment marker name to 1 when on and unsets it
// otherwise, so a hash never inherits its parent hash's marker.
func setMarker(name string, on bool) {
	if on {
		os.Setenv(name, "1")
	} else {
		os.Unsetenv(name)
	}
}

func printHelp() {
	fmt.Println(`Usage: hash [OPTIONS]                              Interactive shell (stdin is a terminal)
       hash [OPTIONS] -c COMMAND [NAME [ARGS...]]   Run COMMAND ($0 is NAME)
       hash [OPTIONS] SCRIPT [ARGS...]              Run the file SCRIPT
       hash [OPTIONS] -s [ARGS...]                  Run the script on stdin
       cmds | hash                                  Run stdin as a script

Options (they end at the first other word, at -- or at -; short ones combine, as in -lc):
  -l, --login     Start as a login shell: read the login files
  -c              Run the first word after the options as a command
  -s              Read the script from stdin; the words after the options are $1...
                  (a stdin that is a terminal is the interactive shell, as in bash)
  -i              Read the interactive startup files (~/.hashrc) before a command or script
  -e, -u, -x, -a, -f, -n, -o NAME, +x, +o NAME, ...
                  bash's set options, applied before the command or script
                  (the interactive shell refuses them: run set there)
  --norc, --noprofile
                  Accepted for compatibility; have no effect
  -v, --version   Show version
  -h, --help      Show this help

Subcommands:
  version         Show version
  migrate         Import compatible bash/zsh settings

Interactive builtins:
  tips            Show common Hash shortcuts and AI syntax
  status          Show subsystem status
  history         Search and inspect command history
  copy            Copy recent command output
  issue           Draft a GitHub issue from shell context
  setup-zoxide    Configure zoxide integration

Non-interactive shells (-c, a script, -s, a stdin that is not a terminal):
  Run bash code with no prompt, editor, agent, history or terminal escapes.
  A command named ?? is refused (status 127); a syntax error ends the shell
  with status 2.

Environment:
  HASH_LOGIN=1        Set in a login shell, unset otherwise
  HASH_INTERACTIVE=1  Set in an interactive shell (the prompt, or -i), unset otherwise
  HASH_SHELL=1        Always set to identify Hash

Startup files (as bash):
  Login shell (-l):  /etc/profile, ~/.profile, ~/.hash_profile, even with -c or a script
  Interactive (-i, or the prompt):  ~/.hashrc, after the login files
  Other shells:      none

Parser dialect (interactive shells):
  Default:           bash
  Optional:          set [shell] dialect = "zsh" in config.toml`)
}

func printVersion() {
	fmt.Printf("hash %s\n", version.String())
}

// runInteractive runs the interactive shell and ends hash.
func runInteractive(mode shell.Mode) {
	// Initialize tracing (before anything else)
	if err := trace.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "hash: trace init failed: %v\n", err)
	}

	// Load configuration (warns but continues on parse errors)
	configDir := config.Dir()
	cfg := config.Load(configDir)
	if cfg.LoadIssue != nil {
		// The config keeps whatever could be salvaged: warn loudly but continue.
		fmt.Fprint(os.Stderr, cfg.LoadIssue.Warning())
	}

	// Create shell with mode
	sh, err := shell.NewWithMode(cfg, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		trace.Close()
		os.Exit(1)
	}

	// A fatal signal (SIGHUP) ends the shell's context; SIGINT (Ctrl+C) is
	// handled locally by the shell components that need it; SIGQUIT and
	// SIGTERM do nothing, as in bash.
	ctx, _ := signalContext(interactiveFatalSignals, nil)
	dropSignals(shellKeeps...)

	err = sh.Run(ctx)
	status := sh.ExitStatus()
	if _, signaled := executor.SignalCause(ctx); err != nil && !signaled {
		// The shell's own context error is not news after a signal.
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		status = 1
	}
	endShell(ctx, status, interactiveCloser(sh, err))
}

// interactiveShell is what hash's end needs of the interactive shell.
type interactiveShell interface {
	CloseOnSignal(sig syscall.Signal) error
	Close() error
	ExitStatus() int
}

// interactiveCloser closes the interactive shell sh as hash ends: its EXIT
// trap runs, after a fatal signal with $? = 128+signal, and hash
// exits with the status the shell reports. runErr is what sh's Run
// returned: an exit in the EXIT trap counts only when Run ended cleanly.
func interactiveCloser(sh interactiveShell, runErr error) shellCloser {
	return shellCloser{close: func(sig syscall.Signal, status int) (int, bool) {
		if sig != 0 {
			_ = sh.CloseOnSignal(sig)
		} else {
			_ = sh.Close()
		}
		trace.Close()
		return sh.ExitStatus(), runErr == nil
	}}
}
