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
```

## Architecture

```
hash/
├── cmd/hash/          # Main entry point
├── internal/
│   ├── parser/        # mvdan/sh wrapper + ?? detection
│   ├── executor/      # Command execution via PTY, job control
│   ├── readline/      # Input handling, completions, keybindings (emacs/vim/helix)
│   ├── agent/         # ACP client with transport abstraction
│   ├── history/       # SQLite storage with FTS5, sudo tracking
│   ├── learning/      # Error pattern matching and fix scoring
│   ├── clipboard/     # Cross-platform copy command/output
│   ├── prompt/        # Starship integration + built-in Lipgloss engine
│   └── config/        # TOML configuration parsing
└── go.mod
```

### Key Design Decisions

**Agent invocation**: `??` prefix (not `#`). Supports both `?? <prompt>` at line start and `cmd | ?? <prompt>` for pipe completion.

**Transport abstraction**: `AgentTransport` interface with `StdioTransport` (Claude Code, Gemini CLI) and `HTTPTransport` (Ollama, local models).

**Completion tiers**: 1) Filesystem (<10ms), 2) Tool-native via Cobra `__complete` (10-200ms), 3) Agent fallback (200-800ms).

**Learning system**: Extracts normalized error signatures, scores fixes by success rate + recency + frequency. Suggests learned fixes when score >= 0.7.

**History**: SQLite with unlimited entries, tracks sudo commands separately, stores agent interactions for recall.

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

## Configuration

User config: `~/.config/hash/config.toml` (TOML format)
See `docs/config-reference.md` for all options.
