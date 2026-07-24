# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Hash (Harness Assisted SHell) is an AI-powered shell written in Go with ACP (Agent Client Protocol) integration. It provides a Warp-like intelligent experience while being agent-agnostic, local-first, and protocol-based.

## Build Commands

```bash
go build -o hash ./cmd/hash      # Build binary
go test ./...                     # Run all tests
go test ./internal/parser/...     # Run tests for a specific package
go test -run TestName ./...       # Run a single test
go vet ./...                      # Lint

# Fuzz testing (parser and learning system)
go test -fuzz=FuzzParse -fuzztime=30s ./internal/parser/...
go test -fuzz=FuzzNormalizeError -fuzztime=30s ./internal/learning/...
```

## Architecture

```
hash/
├── cmd/hash/          # Main entry point
├── internal/
│   ├── agent/         # ACP client, StdioTransport, HTTPTransport
│   ├── clipboard/     # Copy command/output buffer
│   ├── completion/    # Three-tier completion router
│   ├── config/        # TOML configuration parsing
│   ├── context/       # Context picker TUI
│   ├── executor/      # POSIX interpreter (mvdan/sh), PTY-on-demand
│   ├── history/       # SQLite history, sudo tracking
│   ├── learning/      # Error pattern learning, fix suggestions
│   ├── parser/        # ?? detection and command parsing
│   ├── progress/      # OSC 9;4 progress bar support
│   ├── prompt/        # Starship + built-in prompt engines
│   ├── readline/      # Input, Emacs/Vim/Helix keybindings
│   └── shell/         # REPL, builtins, agent integration
└── go.mod
```

### Agent Invocation

Use `??` to invoke the agent:
- `?? find large files` - Full agent request
- `cmd | ?? filter output` - Pipe command output to agent
- `cmd --flag=?? description` - Inline completion

### Key Design Decisions

**Agent invocation**: `??` prefix (not `#`). Supports `?? <prompt>` at line start, `cmd | ?? <prompt>` for pipe completion, and `cmd ?? <prompt>` for inline completion.

**Agent lifecycle**: Agent-per-shell-session. Lazy init on first `??`, kept alive for session, closed on shell exit. No sharing between shell instances.

**Transport abstraction**: `AgentTransport` interface with `StdioTransport` (Claude Code, Gemini CLI) and `HTTPTransport` (Ollama, local models).

**Completion tiers**: 1) Filesystem (<10ms), 2) Tool-native via Cobra `__complete` (10-200ms), 3) Agent fallback (200-800ms).

**Completion plugins**: Declarative TOML specs map subcommands to a candidate-producing command (`internal/completion/plugin.go`). Built-in docker spec uses the same format; users extend via `~/.config/hash/completions/*.toml`. See `docs/completion-plugins.md`.

**Learning system**: Extracts normalized error signatures, scores fixes by success rate + recency + frequency. Suggests learned fixes when score >= 0.7.

**History**: SQLite with unlimited entries, tracks sudo commands separately, stores agent interactions for recall.

**Shell identity**: Sets `$0` to "hash", `$HASH_SHELL=1` as marker, and `$SHELL` when used as login shell.

**Configurable builtins**: Builtins like `cd` can be disabled via config to allow external tools (zoxide, eza).

**Progress bars**: OSC 9;4 escape sequences shown after 2s of command execution in supporting terminals.

**SSH**: Not supported. Hash is designed for local terminal use only.

## Key Dependencies

- `mvdan.cc/sh` — POSIX shell parser/interpreter (core)
- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/charmbracelet/bubbles` — TUI components
- `github.com/charmbracelet/lipgloss` — TUI styling
- `github.com/creack/pty` — PTY handling
- `github.com/mattn/go-sqlite3` — History storage
- `golang.design/x/clipboard` — Cross-platform clipboard

## Version Control

This project uses **jj (Jujutsu)** instead of git. Key commands:

```bash
jj status              # Show working copy state
jj log                 # Show commit history
jj describe -m "..."   # Set commit message (no staging needed)
jj new                 # Create new commit on top
jj diff                # Show changes
```

**Important**: When committing, always commit to both jj and git.

## Configuration

User config: `~/.config/hash/config.toml` (TOML format)
See `docs/config-reference.md` for all options.
