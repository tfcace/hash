# Hash

[![Go Report Card](https://goreportcard.com/badge/github.com/tfcace/hash)](https://goreportcard.com/report/github.com/tfcace/hash)
[![Go Reference](https://pkg.go.dev/badge/github.com/tfcace/hash.svg)](https://pkg.go.dev/github.com/tfcace/hash)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**Harness Assisted SHell** — a Go shell with editor-style input, Helix keybindings, and agent-agnostic AI integration.

> **⚠️ Early Stage Project**
>
> Hash is under active development. Expect bugs and breaking changes before 1.0.
> The 0.6 line adds turn-by-turn agent conversations, experimental zsh dialect
> support, and a refreshed Claude ACP adapter setup.

## What is Hash?

Hash is a shell that treats AI as a first-class citizen without locking you into any particular model or vendor. Drop `??` anywhere in a command to get help:

```bash
# Generate a command from natural language
?? find all Go files modified today
→ find . -name "*.go" -mtime 0
  [Enter: run] [Tab: edit] [Esc: cancel]

# Pipe output through the agent
kubectl get pods -o json | ?? extract names and status
→ jq -r '.items[] | "\(.metadata.name) \(.status.phase)"'

# Fill just one argument
git log --format=?? oneline with hash
→ git log --format="%h %s"
```

No mode switching. No special commands. Just `??` where you need help. And when the agent answers with a question, Hash opens a conversation prompt so you can reply turn by turn in the same session.

## Highlights

- **Agent-agnostic** — any ACP agent (Claude, Gemini CLI) over stdio, or Ollama-style HTTP model servers.
- **Explicit permissions** — agent tool calls prompt allow/deny/always, with approvals scoped per project, globally, or per session.
- **Turn-by-turn conversations** *(new in 0.6)* — follow-up questions continue in the same agent session; leave with Esc, `/exit`, or just "done".
- **Context you control** — the Ctrl+P picker decides exactly what is sent to the agent.
- **Learns locally** — repeated error fixes are suggested instantly, no agent call, nothing leaves your machine.
- **Editor-style input** — multiline editing, visual selection, Helix/Vim/Emacs keybindings.
- **Smart completion** — tool-native (Cobra), aliases and functions, env vars, files, agent fallback.
- **Rich history** — SQLite with full-text search, sudo tracking, and agent-interaction recall.
- **Plays nice** — Starship prompts, shell-integration escapes (OSC 133), zoxide/direnv/fzf setup, configurable builtins.
- **Experimental zsh dialect** *(new in 0.6)* — parse commands and startup files as zsh via `shell.dialect`.

Full guides, tutorials, and troubleshooting live at **[runhash.dev](https://runhash.dev/)**.

## Installation

### Homebrew (recommended)

```bash
brew tap tfcace/hash
brew install hash
```

### From source

Requires Go 1.25+ and a C compiler (for SQLite).

```bash
git clone https://github.com/tfcace/hash.git
cd hash
go build -o /usr/local/bin/hash ./cmd/hash
```

## Configure an agent

For Claude over ACP, install the adapter:

```bash
npm install -g @agentclientprotocol/claude-agent-acp
```

Then point Hash at it in `~/.config/hash/config.toml`:

```toml
[agent]
transport = "stdio"
command = "claude-agent-acp"
```

Authenticate with `ANTHROPIC_API_KEY`, or with your Claude subscription: on macOS, the adapter reuses the OAuth credentials from your Claude Code CLI login automatically.

Hash also supports named agent profiles and Ollama-style HTTP servers. See [docs/config-reference.md](docs/config-reference.md) for every option, or the [agents guide](https://runhash.dev/docs/agents) on the website.

## Using as login shell

```bash
sudo sh -c 'echo $(which hash) >> /etc/shells'
chsh -s $(which hash)
```

Login shells source `/etc/profile`, `~/.profile`, and `~/.hash_profile`; interactive shells source `~/.hashrc`. Hash parses shell code as bash by default — set `shell.dialect = "zsh"` (experimental) to parse zsh syntax and source your zsh startup files directly. See the [config reference](docs/config-reference.md) for details.

On first launch, Hash detects your previous shell and asks before loading compatible settings. Run `hash migrate` to re-run migration, or `hash migrate status` to see what was imported and skipped.

## Documentation

- **[runhash.dev](https://runhash.dev/)** — guides for syntax, agents, context, completion, learning, keybindings, integrations, migration, and advanced debugging (tracing, PTY logs, debug env vars).
- **[docs/config-reference.md](docs/config-reference.md)** — the complete configuration reference.

## Development

```bash
go build -o hash ./cmd/hash    # Build
./scripts/build.sh             # Build with version info (--install for /usr/local/bin)
go test ./...                   # Run all tests
go vet ./...                    # Lint
go test -fuzz=Fuzz -fuzztime=30s ./internal/...   # Fuzz parser and learning
```

```
hash/
├── cmd/hash/          # Main entry point
├── internal/
│   ├── parser/        # mvdan/sh wrapper + ?? detection
│   ├── executor/      # Command execution via PTY, job control
│   ├── readline/      # Input handling, completions, keybindings
│   ├── agent/         # ACP client with transport abstraction
│   ├── history/       # SQLite storage with FTS5, sudo tracking
│   ├── learning/      # Error pattern matching and fix scoring
│   ├── clipboard/     # Cross-platform copy command/output
│   ├── prompt/        # Starship integration + built-in engine
│   └── config/        # TOML configuration parsing
└── go.mod
```

Key dependencies: `mvdan.cc/sh` (POSIX/zsh parsing and interpretation), `charmbracelet/bubbletea` and `lipgloss` (TUI), `creack/pty`, `mattn/go-sqlite3`.

This project uses [jj (Jujutsu)](https://github.com/martinvonz/jj) for version control.

## Status & limitations

- **Tested on** — macOS + Ghostty + Claude (ACP via `claude-agent-acp`) only. Other platforms, terminals, and agents may work but are untested.
- **Stability** — There will be bugs. Lots of them. File issues for anything broken.
- **Performance** — Built for responsiveness, not raw throughput. Go is fast enough for a shell, but this isn't a Rust rewrite of bash.
- **Scope** — This is shaped around my preferences (Helix keybindings, local-first, agent-agnostic). It may not fit yours.
- **SSH** — Not supported. Hash is designed for local terminal use only.

## See also

**[Warp](https://www.warp.dev/)** pioneered the modern terminal experience. Hash borrows ideas but takes a different path: Hash is a shell that runs in any terminal (not a macOS app), works with any agent (not a built-in one), and keeps everything local and open source. If you want a polished, integrated product, Warp is excellent. If you want hackability and agent choice, that's Hash.

**[Butterfish](https://butterfi.sh)** wraps your existing shell with OpenAI-powered help; Hash replaces the shell, is agent-agnostic, and learns from your error fixes locally. Choose Butterfish for a quick overlay without switching shells; choose Hash for deeper integration and local-first data.

## License

MIT
