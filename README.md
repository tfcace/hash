# Hash

**Harness Assisted SHell** — A Go-based shell with editor-style input, Helix keybindings, and agent-agnostic AI integration.

> **⚠️ Early Stage Project**
>
> Hash is a heavily vibe-coded experiment built mostly by Claude. Expect bugs, rough edges, and breaking changes. This is a personal project shaped around my own preferences—not a polished tool for everyone. If that sounds fun, welcome aboard.

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

No mode switching. No special commands. Just `??` where you need help.

## Features

### Agent Agnostic

Use any AI that speaks ACP or HTTP. Configure Claude Code for powerful cloud AI, or Ollama for fully local inference:

```toml
[agent.claude]
transport = "stdio"
command = "claude"

[agent.ollama]
transport = "http"
url = "http://localhost:11434/api/generate"
model = "codellama:13b"
```

### Adaptive Learning

Hash learns from how you fix errors. After seeing you run `chmod +x` a few times after "Permission denied", it suggests the fix instantly — no agent call needed:

```
$ ./deploy.sh
bash: ./deploy.sh: Permission denied

✗ Exit 126 │ Learned fix available

→ chmod +x ./deploy.sh    (worked 12 times)
  [Enter: run] [Tab: edit] [?: ask agent] [Esc: dismiss]
```

### Context Picker (Ctrl+E)

Control exactly what context goes to the agent with an interactive TUI:

```
┌─ Context for Agent ─────────────────────────────────────┐
│ ▸ History                                    [3 items]  │
│   [✓] kubectl get pods -n staging                       │
│   [✓] docker ps --format "{{.Names}}"                   │
│   [ ] cd ~/projects/hash                                │
│                                                         │
│ ▸ Environment                                [2 items]  │
│   [✓] KUBECONFIG=/Users/me/.kube/config                 │
│   [ ] AWS_PROFILE=production                            │
├─────────────────────────────────────────────────────────┤
│  Context: ████████░░░░░░  2.1 KB / ~8 KB recommended    │
└─────────────────────────────────────────────────────────┘
```

### Editor-Style Input

The prompt behaves like a code editor, not a line input. Multiline editing, visual selection, and full keyboard navigation — just like Warp:

```
$ docker run -d \
    --name myapp \
    -p 8080:80 \
    -v $(pwd):/app \
    myimage:latest
│ ← visual gutter shows editable area
```

- **Multiline native** — Enter inserts newlines, Escape → Enter submits (in Helix mode)
- **Visual selection** — Select text with keyboard, yank/paste with system clipboard
- **Helix keybindings** — Selection-first editing: `x` selects line, `w` extends to word, `d` deletes selection
- **Universal shortcuts** — Ctrl+A/E, Alt+arrows work everywhere
- **Sub-millisecond latency** — Raw terminal I/O, feels identical to readline

```toml
[input]
keybindings = "helix"  # or "emacs" (coming soon)
gutter = true          # show visual indicator
```

### Three-Tier Completions

Fast completions with intelligent fallback:

1. **Filesystem** (<10ms) — paths, globs, hidden file toggle
2. **Tool-native** (10-200ms) — Cobra `__complete` detection for kubectl, docker, helm, etc.
3. **Agent-assisted** (200-800ms) — when you need AI help mid-command

### Rich History with Full-Text Search

SQLite-backed history with sudo tracking, agent interaction recall, and FTS5 search:

```bash
$ history search "kubectl delete"
$ history failed                    # commands that failed
$ history sudo                      # privileged commands
$ history asked "docker"            # what you asked the AI
```

### Starship Prompt Support

Use your existing [Starship](https://starship.rs) configuration, or Hash's built-in Lipgloss-powered engine with Powerline support:

```toml
[prompt]
mode = "starship"  # or "built-in"
```

### Clipboard Integration

Copy commands and output without reaching for the mouse:

| Key | Action |
|-----|--------|
| `Alt+c` | Copy last command |
| `Alt+o` | Copy last output |
| `Alt+Shift+c` | Copy command + output |

### Progress Bars for Long Commands

Hash shows progress indication (OSC 9;4) in supporting terminals when commands run longer than 2 seconds. Works with Windows Terminal, ConEmu, and iTerm2.

### Configurable Builtins

Disable built-in commands to use external alternatives like [zoxide](https://github.com/ajeetdsouza/zoxide) or [eza](https://github.com/eza-community/eza):

```toml
[shell]
disable_builtins = ["cd"]  # Let zoxide handle cd
```

## Installation

```bash
# Build from source
go build -o hash ./cmd/hash

# Run
./hash
```

## Configuration

Hash uses TOML configuration at `~/.config/hash/config.toml`:

```toml
[shell]
editor = "hx"
keybindings = "helix"

[agent]
default = "claude"
timeout = "30s"

[agent.claude]
transport = "stdio"
command = "claude"

[completions]
fuzzy = true
file_icons = true

[learning]
enabled = true
suggestion_threshold = 0.7
```

See [docs/config-reference.md](docs/config-reference.md) for the complete reference.

## Using as Login Shell

### Installation

1. Build and install:
   ```bash
   go build -o /usr/local/bin/hash ./cmd/hash
   ```

2. Add to `/etc/shells`:
   ```bash
   sudo sh -c 'echo /usr/local/bin/hash >> /etc/shells'
   ```

3. Change your login shell:
   ```bash
   chsh -s /usr/local/bin/hash
   ```

### Startup Files

Hash follows zsh-style conventions for startup files:

| Mode | Environment | Files Sourced |
|------|-------------|---------------|
| Login | `HASH_LOGIN=1` | `/etc/profile`, `~/.profile`, `~/.hash_profile` |
| Interactive | `HASH_INTERACTIVE=1` | `~/.hashrc` |
| Login+Interactive | Both | All of the above, in order |
| Non-interactive | Neither | `init_commands` only |

**Recommended setup:**

```bash
# ~/.hash_profile - login shell setup (PATH, env vars)
export PATH="$HOME/.local/bin:$PATH"
export EDITOR=hx

# ~/.hashrc - interactive shell setup (aliases, functions)
alias ll='ls -la'
alias g='git'
```

### Session Markers

Hash sets these environment variables:
- `HASH_SHELL=1` — Always set
- `HASH_LOGIN=1` — Set for login shells
- `HASH_INTERACTIVE=1` — Set for interactive shells

Use these in your scripts to detect Hash:

```bash
# In ~/.profile
if [ "$HASH_SHELL" = "1" ]; then
    # Hash-specific setup
fi
```

## Architecture

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

### Key Dependencies

- **mvdan.cc/sh** — POSIX shell parser/interpreter
- **charmbracelet/bubbletea** — TUI framework
- **charmbracelet/lipgloss** — Styling
- **creack/pty** — PTY handling
- **mattn/go-sqlite3** — History storage

## Development

```bash
go build -o hash ./cmd/hash    # Build
go test ./...                   # Run all tests
go vet ./...                    # Lint
```

This project uses [jj (Jujutsu)](https://github.com/martinvonz/jj) for version control.

## Advanced

### Debug Environment Variables

These environment variables are for debugging and advanced use cases:

| Variable | Description | Default |
|----------|-------------|---------|
| `HASH_CLIPBOARD_MAX_OUTPUT_SIZE` | Maximum output capture per command. Accepts sizes like `1MB`, `512KB`, `unlimited`, or `0` to disable capture. | `1MB` |
| `HASH_PTY_TRACE` | Enable PTY I/O tracing for debugging hangs. Set to `1` or `true` to enable. | Disabled |
| `HASH_PTY_TRACE_PATH` | Path for PTY trace log file when tracing is enabled. | `./hash-pty-trace.log` |

**Example: Debugging PTY issues**

```bash
# Enable PTY tracing to diagnose hangs
export HASH_PTY_TRACE=1
export HASH_PTY_TRACE_PATH=/tmp/hash-pty.log
./hash

# After reproducing the issue, check the log
cat /tmp/hash-pty.log
```

**Example: Adjust output capture**

```bash
# Disable output capture entirely
export HASH_CLIPBOARD_MAX_OUTPUT_SIZE=0

# Capture unlimited output (use with caution)
export HASH_CLIPBOARD_MAX_OUTPUT_SIZE=unlimited
```

## Status & Limitations

Hash is under active development. Here's what to expect:

- **Tested on** — macOS + Ghostty + Claude Code (with ACP) only. Other platforms, terminals, and agents may work but are untested.
- **Stability** — There will be bugs. Lots of them. File issues for anything broken.
- **Performance** — Built for responsiveness, not raw throughput. Go is fast enough for a shell, but this isn't a Rust rewrite of bash.
- **Scope** — This is shaped around my preferences (Helix keybindings, local-first, agent-agnostic). It may not fit yours.
- **SSH** — Not supported. Hash is designed for local terminal use only.

## See Also

### Warp

[Warp](https://www.warp.dev/) pioneered the modern terminal experience with blocks, AI, and editor-style input. Hash borrows ideas from Warp but takes a different path:

- Warp is a macOS app; Hash is a cross-platform shell that runs in any terminal
- Warp has its own AI; Hash works with any agent (Claude, Ollama, Gemini, local models)
- Warp syncs to the cloud; Hash keeps everything local
- Warp is proprietary; Hash is open source

If you want a polished, integrated experience and don't mind the lock-in, Warp is excellent. If you want hackability and agent choice, that's what Hash is for.

### Butterfish

[Butterfish](https://butterfi.sh) is an AI shell wrapper worth considering:

- Butterfish wraps your existing shell; Hash replaces it
- Butterfish uses OpenAI; Hash is agent-agnostic
- Butterfish has a "goal mode" for autonomous execution; Hash requires explicit confirmation
- Butterfish has no local learning; Hash learns from your error fixes over time

**Choose Butterfish** if you want a quick overlay without switching shells, or prefer autonomous AI execution.

**Choose Hash** if you want deeper integration, agent flexibility, or local-first data.

## License

MIT
