# Hash Configuration Reference

Hash is configured via TOML files in `~/.config/hash/`.

## File Locations

| File | Purpose |
|------|---------|
| `~/.config/hash/config.toml` | Main configuration |
| `~/.config/hash/aliases.toml` | Command aliases |
| `~/.local/share/hash/history.db` | Command history (SQLite) |

---

## [shell]

Core shell behavior settings.

### shell.editor

**Type:** `string`
**Default:** `$EDITOR` or `"nano"`

The editor launched when pressing Tab to edit a suggested command, or for
multi-line editing. Supports any terminal editor.

```toml
editor = "micro"      # Lightweight, user-friendly
editor = "hx"         # Helix
editor = "nvim"       # Neovim
editor = "$EDITOR"    # Use environment variable
```

### shell.keybindings

**Type:** `"emacs"` | `"vim"` | `"helix"`
**Default:** `"emacs"`

The keybinding style for command-line editing.

- **emacs**: Traditional readline bindings. Ctrl+A/E for line start/end,
  Ctrl+W to delete word, Ctrl+R for history search.

- **vim**: Modal editing. Starts in insert mode, Esc to normal mode.
  hjkl navigation, w/b for word movement, standard vim operators.

- **helix**: Selection-first modal editing. Like vim, but movements in
  select mode (v) extend the selection. Actions operate on selections.
  Uses `gh`/`gl` for line start/end instead of `0`/`$`.

```toml
keybindings = "helix"
```

### shell.init_commands

**Type:** `array of strings`
**Default:** `[]`

Commands executed when Hash starts. Use for sourcing additional configs,
setting environment variables, or running startup checks.

```toml
init_commands = [
    "source ~/.config/hash/aliases.sh",
    "export GPG_TTY=$(tty)",
]
```

### shell.disable_builtins

**Type:** `array of strings`
**Default:** `[]`

Built-in commands to disable. When disabled, Hash falls through to external
command execution, allowing tools like zoxide, eza, or custom wrappers.

```toml
disable_builtins = ["cd"]        # Use zoxide for cd
disable_builtins = ["cd", "pwd"] # Use external implementations
```

Currently disableable builtins:
- `cd` — change directory (for zoxide, z, etc.)
- `pwd` — print working directory
- `exit` — exit shell (not recommended to disable)

### shell.profile

**Type:** `array of strings`
**Default:** `[]`

Commands executed when Hash starts as a login shell (before rc_commands).
Use for environment setup that should only happen once per login session.

```toml
profile = [
    "export PATH=$HOME/.local/bin:$PATH",
    "export EDITOR=hx",
]
```

### shell.rc_commands

**Type:** `array of strings`
**Default:** `[]`

Commands executed when Hash starts as an interactive shell.
Use for aliases, prompt customization, and per-shell setup.

```toml
rc_commands = [
    "alias ll='ls -la'",
    "alias g='git'",
]
```

### shell.startup_files

**Type:** `table`
**Default:** See below

Files to source at startup, depending on shell mode.

```toml
[shell.startup_files]
# Login shell files (sourced in order)
login = [
    "/etc/profile",
    "~/.profile",
    "~/.hash_profile",
]

# Interactive shell files (sourced after login files if applicable)
interactive = [
    "~/.hashrc",
]
```

**Startup Order:**

1. **Login shell** (`hash -l` or via `/etc/passwd`):
   - Source files in `startup_files.login`
   - Run `shell.profile` commands
   - (If also interactive) Source files in `startup_files.interactive`
   - (If also interactive) Run `shell.rc_commands`
   - Run `shell.init_commands` (always)

2. **Interactive shell** (non-login):
   - Source files in `startup_files.interactive`
   - Run `shell.rc_commands`
   - Run `shell.init_commands` (always)

3. **Non-interactive shell** (`hash -c "command"`):
   - Run `shell.init_commands` only

---

## [prompt]

Prompt appearance and behavior.

### prompt.mode

**Type:** `"starship"` | `"built-in"` | `"none"`
**Default:** `"starship"` if installed, otherwise `"built-in"`

Which prompt engine to use.

- **starship**: Use the [Starship](https://starship.rs) prompt. Recommended.
  Starship must be installed separately. Configured via `~/.config/starship.toml`.

- **built-in**: Hash's native prompt engine. Supports colors, Nerd Font symbols,
  and Powerline-style segments. Configured in `[prompt.theme]` and `[prompt.format]`.

- **none**: Minimal prompt showing only `$ `. Useful for scripting or
  embedding Hash.

```toml
mode = "starship"
```

### prompt.starship_path

**Type:** `string`
**Default:** auto-detected from `$PATH`

Explicit path to the Starship binary. Only needed if Starship is installed
in a non-standard location.

```toml
starship_path = "/opt/homebrew/bin/starship"
```

### prompt.dev_mode

**Type:** `boolean`
**Default:** `false`

Show a development/non-production indicator chip in the prompt. The chip
appears on the opposite side of the prompt alignment:
- Left-aligned prompt → chip on right
- Right-aligned prompt → chip on left

```toml
dev_mode = true
```

### prompt.dev_mode_label

**Type:** `string`
**Default:** `"dev"`

Text shown in the dev mode chip.

```toml
dev_mode_label = "dev"
dev_mode_label = "DEV"
dev_mode_label = "⚠ dev"
```

---

## [prompt.theme]

Color theme for the built-in prompt engine. Ignored if `prompt.mode = "starship"`.

All colors accept:
- Hex codes: `"#F4AE59"`
- ANSI color names: `"red"`, `"bright-blue"`
- ANSI 256 codes: `"178"`

### Available theme keys

```toml
[prompt.theme]
# Segment backgrounds
bg_user = "#241417"       # Username segment
bg_dir = "#F4AE59"        # Directory segment
bg_git = "#20352A"        # Git branch segment
bg_kube = "#1F2E2B"       # Kubernetes context segment
bg_time = "#1A1F1B"       # Time segment

# Segment foregrounds
fg_user = "#c27166"
fg_dir = "#0F1B1D"
fg_git = "#e0f0ef"
fg_kube = "#e09785"
fg_time = "#e0f0ef"

# Status indicators
success = "#8dac8b"       # Prompt character on exit 0
error = "#c27166"         # Prompt character on non-zero exit
```

---

## [prompt.format]

Layout for the built-in prompt engine.

### prompt.format.left

**Type:** `string`
**Default:** `"[ {cwd} ][ {git_branch} ]"`

Left-side prompt format. Supports Powerline transitions with `[](fg:X bg:Y)`.

**Available variables:**

| Variable | Description |
|----------|-------------|
| `{cwd}` | Current working directory |
| `{cwd_basename}` | Only the current directory name |
| `{git_branch}` | Current git branch |
| `{git_status}` | Git status indicators (+, !, ?) |
| `{user}` | Current username |
| `{host}` | Hostname |
| `{kube_context}` | Kubernetes context name |
| `{kube_namespace}` | Kubernetes namespace |
| `{time}` | Current time (HH:MM) |
| `{duration}` | Last command duration |
| `{exit_code}` | Last command exit code |
| `{jobs}` | Background job count |

**Powerline example:**

```toml
left = """
[](bg:bg_dir)\
[ {cwd} ](bg:bg_dir fg:fg_dir)\
[](bg:bg_git fg:bg_dir)\
[ {git_branch} ](bg:bg_git fg:fg_git)\
[](fg:bg_git)"""
```

### prompt.format.right

**Type:** `string`
**Default:** `""`

Right-side prompt format. Same variables as `left`. Rendered at terminal's
right edge.

```toml
right = "[ {kube_context} ]  [ {time} ]"
```

### prompt.format.newline

**Type:** `boolean`
**Default:** `false`

Whether to print the prompt character on a new line below the info segments.

```toml
newline = true
```

Produces:
```
~/projects/hash  main
❯
```

### prompt.format.character

**Type:** `string`
**Default:** `"❯"`

The prompt character shown before user input. Colored by `success`/`error`
theme colors based on last command's exit code.

```toml
character = "λ"
```

---

## [agent]

AI agent configuration for `??` commands.

### agent.default

**Type:** `string`
**Default:** `"claude"`

Which configured agent to use by default. Must match a key in `[agent.<name>]`.

```toml
default = "claude"
```

### agent.timeout

**Type:** `duration string`
**Default:** `"30s"`

Maximum time to wait for an agent response before timing out.

```toml
timeout = "30s"
timeout = "1m"
timeout = "2m30s"
```

---

## [agent.<name>]

Configure individual agents. Create multiple sections for different agents.

### agent.<name>.transport

**Type:** `"stdio"` | `"http"`
**Required**

How to communicate with the agent.

- **stdio**: Launch agent as subprocess, communicate via JSON-RPC over
  stdin/stdout. Used for Claude Code, Gemini CLI, local wrappers.

- **http**: Communicate via HTTP API. Used for Ollama, LM Studio, vLLM,
  and other HTTP-based inference servers.

### agent.<name>.command (stdio only)

**Type:** `string`
**Required for stdio transport**

The command to launch the agent subprocess.

```toml
[agent.claude]
transport = "stdio"
command = "claude"
```

### agent.<name>.args (stdio only)

**Type:** `array of strings`
**Default:** `[]`

Arguments passed to the agent command.

```toml
[agent.claude]
transport = "stdio"
command = "claude"
args = ["--chat", "--no-banner"]
```

### agent.<name>.url (http only)

**Type:** `string (URL)`
**Required for http transport**

The base URL for the agent's HTTP API.

```toml
[agent.ollama]
transport = "http"
url = "http://localhost:11434/api/generate"
```

### agent.<name>.model (http only)

**Type:** `string`
**Default:** agent-specific

The model name to use with HTTP-based agents.

```toml
[agent.ollama]
transport = "http"
url = "http://localhost:11434/api/generate"
model = "codellama:13b"
```

### agent.<name>.headers (http only)

**Type:** `table of strings`
**Default:** `{}`

Additional HTTP headers for API requests. Useful for authentication.

```toml
[agent.custom]
transport = "http"
url = "https://api.example.com/v1/chat"
model = "gpt-4"
headers = { Authorization = "Bearer ${OPENAI_API_KEY}" }
```

---

## [agent.context]

Default context sent to agents with `??` requests.

### agent.context.include_cwd

**Type:** `boolean`
**Default:** `true`

Include the current working directory.

### agent.context.include_git_branch

**Type:** `boolean`
**Default:** `true`

Include the current git branch (if in a git repository).

### agent.context.include_kube_context

**Type:** `boolean`
**Default:** `true`

Include the current Kubernetes context (if kubectl is configured).

### agent.context.include_env_vars

**Type:** `array of strings`
**Default:** `["KUBECONFIG", "AWS_PROFILE", "NODE_ENV"]`

Environment variables to include in agent context. Only variables listed
here are sent — not your entire environment.

```toml
include_env_vars = [
    "KUBECONFIG",
    "AWS_PROFILE",
    "AWS_REGION",
    "NODE_ENV",
    "RAILS_ENV",
    "GOPATH",
]
```

### agent.context.history_count

**Type:** `integer`
**Default:** `5`

Number of recent commands to include in agent context.

```toml
history_count = 10
```

---

## [completions]

Tab completion behavior.

### completions.fuzzy

**Type:** `boolean`
**Default:** `true`

Enable fuzzy matching in completions. With fuzzy enabled, typing `kgp`
matches `kubectl get pods`.

```toml
fuzzy = true
```

### completions.file_icons

**Type:** `boolean`
**Default:** `true`

Show file type icons in completion menus. Requires a Nerd Font.

```toml
file_icons = true
```

Icons shown:
-  directories
-  text/code files
-  executables
-  symlinks

### completions.show_hidden

**Type:** `boolean`
**Default:** `false`

Show hidden files (dotfiles) in completions by default. Toggle with
Ctrl+H during completion regardless of this setting.

```toml
show_hidden = false
```

---

## [completions.<tool>]

Tool-specific completion settings.

### completions.<tool>.cache_ttl

**Type:** `duration string`
**Default:** varies by tool

How long to cache dynamic completions (pod names, container IDs, etc.).

```toml
[completions.kubectl]
cache_ttl = "5m"      # Pod names don't change often

[completions.docker]
cache_ttl = "30s"     # Containers change more frequently

[completions.aws]
cache_ttl = "15m"     # AWS resources are slow to query
```

### completions.<tool>.enabled

**Type:** `boolean`
**Default:** `true`

Disable completions for a specific tool.

```toml
[completions.terraform]
enabled = false       # Terraform completions are slow, disable them
```

---

## [history]

Command history storage and retention.

### history.enabled

**Type:** `boolean`
**Default:** `true`

Enable command history recording.

```toml
enabled = true
```

### history.path

**Type:** `string (path)`
**Default:** `"~/.local/share/hash/history.db"`

Path to the SQLite history database.

```toml
path = "~/.local/share/hash/history.db"
```

### history.max_entries

**Type:** `integer` | `"unlimited"`
**Default:** `"unlimited"`

Maximum number of history entries to retain. Oldest entries are pruned
first when limit is reached.

```toml
max_entries = "unlimited"    # Never prune by count
max_entries = 100000         # Keep last 100k commands
```

### history.max_age

**Type:** `duration string` | `"forever"`
**Default:** `"forever"`

Maximum age of history entries. Entries older than this are pruned.

```toml
max_age = "forever"          # Never prune by age
max_age = "365d"             # Prune entries older than 1 year
max_age = "90d"              # Prune entries older than 90 days
```

### history.store_agent_interactions

**Type:** `boolean`
**Default:** `true`

Store `??` prompts and agent responses in history. Enables queries like
"what did I ask the agent about docker?"

```toml
store_agent_interactions = true
```

### history.import_system_sudo

**Type:** `boolean`
**Default:** `false`

Attempt to import sudo commands from system logs. Captures privileged
commands run in other shells. Requires read access to sudo logs.

```toml
import_system_sudo = true
```

### history.sudo_log_path

**Type:** `string (path)`
**Default:** auto-detected

Path to system sudo log. Only used if `import_system_sudo = true`.

```toml
sudo_log_path = "/var/log/auth.log"     # Debian/Ubuntu
sudo_log_path = "/var/log/secure"       # RHEL/CentOS
```

### history.exclude_patterns

**Type:** `array of regex strings`
**Default:** `[".*password.*", ".*secret.*", ".*token.*"]`

Commands matching these patterns are not recorded in history. Patterns
are case-insensitive regex matched against the full command.

```toml
exclude_patterns = [
    ".*password.*",
    ".*secret.*",
    ".*token.*",
    "^export .*=",         # Don't log exports (may contain secrets)
    ".*--apikey.*",
    "^vault .*",           # Don't log Vault commands
]
```

---

## [errors]

Error handling and agent assistance on command failure.

### errors.auto_prompt

**Type:** `boolean`
**Default:** `true`

Show a prompt to invoke the agent after command failures. Displays
"?? to explain" after non-zero exit codes.

```toml
auto_prompt = true
```

### errors.ignore_exit_codes

**Type:** `array of integers`
**Default:** `[1]`

Exit codes that should not trigger the agent prompt. Exit code 1 is
commonly used for "not found" (grep, find) which isn't really an error.

```toml
ignore_exit_codes = [1, 2]    # 1 = not found, 2 = misuse (for some tools)
```

### errors.ignore_commands

**Type:** `array of strings`
**Default:** `["grep", "diff", "test", "["]`

Commands that should never trigger the agent prompt on failure. These
tools commonly exit non-zero for non-error conditions.

```toml
ignore_commands = [
    "grep",      # Exit 1 = no match
    "diff",      # Exit 1 = files differ
    "test",      # Exit 1 = condition false
    "[",         # Same as test
    "rg",        # Same as grep
]
```

---

## [learning]

Adaptive error fix suggestions based on past behavior.

### learning.enabled

**Type:** `boolean`
**Default:** `true`

Enable learning from user fix patterns. When you fix an error, Hash
remembers and suggests the same fix for similar errors in the future.

```toml
enabled = true
```

### learning.min_occurrences

**Type:** `integer`
**Default:** `2`

Minimum times a fix must succeed before Hash suggests it automatically.
Prevents suggesting one-off fixes.

```toml
min_occurrences = 2
```

### learning.suggestion_threshold

**Type:** `float (0.0 - 1.0)`
**Default:** `0.7`

Confidence score required to suggest a learned fix without calling the
agent. Higher values require more consistent success history.

```toml
suggestion_threshold = 0.7
```

### learning.auto_apply

**Type:** `boolean`
**Default:** `false`

**Dangerous.** Automatically apply learned fixes without confirmation
when confidence exceeds `auto_apply_threshold`. Not recommended.

```toml
auto_apply = false
```

### learning.auto_apply_threshold

**Type:** `float (0.0 - 1.0)`
**Default:** `0.95`

Confidence score required for auto-applying fixes. Only used if
`auto_apply = true`.

```toml
auto_apply_threshold = 0.95
```

---

## [clipboard]

Clipboard integration for copying commands and output.

### clipboard.max_output_size

**Type:** `size string`
**Default:** `"1MB"`

Maximum output size to keep in memory per command. Larger outputs are
truncated. Use `unlimited` to keep the full output (can grow large), or `0`
to disable output capture.

```toml
max_output_size = "1MB"
max_output_size = "512KB"
max_output_size = "unlimited"
max_output_size = "0"
```

### clipboard.buffer_size

**Type:** `integer`
**Default:** `100`

Number of recent commands (with outputs) to keep in memory for copying.

```toml
buffer_size = 100
```

### clipboard.preserve_colors

**Type:** `boolean`
**Default:** `false`

Include ANSI escape codes when copying output. Useful if pasting into
another terminal, but may cause issues in other applications.

```toml
preserve_colors = false
```

### clipboard.keys

Keybindings for clipboard operations.

```toml
[clipboard.keys]
copy_command = "alt+c"
copy_output = "alt+o"
copy_both = "alt+shift+c"
```

---

## [prediction]

Command and path prediction based on usage patterns.

### prediction.enabled

**Type:** `boolean`
**Default:** `true`

Enable command prediction. When enabled, Hash learns command sequences and suggests the next likely command as ghost text after each successful command.

```toml
enabled = true
```

### prediction.accept_keys

**Type:** `array of strings`
**Default:** `["right", "tab"]`

Keys that accept the predicted ghost text.

```toml
accept_keys = ["right", "tab"]
```

### prediction.confidence_threshold

**Type:** `float (0.0 - 1.0)`
**Default:** `0.6`

Minimum confidence score required to show a prediction. Higher values show fewer but more reliable predictions.

```toml
confidence_threshold = 0.6
```

### prediction.path_min_count

**Type:** `integer`
**Default:** `2`

Minimum times a path must be used with a command before it's suggested in completions.

```toml
path_min_count = 2
```

### prediction.path_recency_boost_hours

**Type:** `integer`
**Default:** `24`

Time window (in hours) for recency boost in prediction scoring. More recently used patterns score higher.

```toml
path_recency_boost_hours = 24
```

---

## Shell Integration

Hash automatically emits terminal integration escape sequences:

- **OSC 133** — Semantic prompt markers for navigation and output selection
- **OSC 7** — Working directory reporting for new tab/pane inheritance
- **OSC 9;4** — Progress indication for agent requests

These sequences are safely ignored by terminals that don't support them. No configuration is required.

**Supported terminals:** iTerm2, Ghostty, Kitty, WezTerm, Windows Terminal, VS Code

---

## [ui]

User interface settings.

### ui.context_picker_key

**Type:** `string (key binding)`
**Default:** `"ctrl+p"`

Key to open the context picker before sending a `??` request.

```toml
context_picker_key = "ctrl+p"
```

### ui.completion_max_height

**Type:** `integer`
**Default:** `10`

Maximum number of rows shown in completion menus.

```toml
completion_max_height = 15
```

### ui.progress_bar_delay

**Type:** `duration string`
**Default:** `"2s"`

How long a command must run before showing progress indication (OSC 9;4).
Set to `"0s"` to always show, or a high value to effectively disable.

Requires a supporting terminal (Windows Terminal, ConEmu, iTerm2).

```toml
progress_bar_delay = "2s"     # Show after 2 seconds
progress_bar_delay = "5s"     # Wait longer before showing
progress_bar_delay = "0s"     # Always show progress
progress_bar_delay = "1h"     # Effectively disable
```

---

## [ui.colors]

UI element colors (outside of prompt).

### ui.colors.suggestion

**Type:** `color string`
**Default:** `"#888888"`

Color for agent suggestions and ghost text.

### ui.colors.error

**Type:** `color string`
**Default:** `"#c27166"`

Color for error messages.

### ui.colors.success

**Type:** `color string`
**Default:** `"#8dac8b"`

Color for success messages.

### ui.colors.info

**Type:** `color string`
**Default:** `"#93bfc2"`

Color for informational messages.

```toml
[ui.colors]
suggestion = "#888888"
error = "#c27166"
success = "#8dac8b"
info = "#93bfc2"
```

---

## [keybindings]

Custom keybinding configuration.

### keybindings.style

**Type:** `"emacs"` | `"vim"` | `"helix"`
**Default:** same as `shell.keybindings`

Base keybinding style. Can be overridden from `shell.keybindings` if you
want different behavior for navigation vs. editing.

### keybindings.overrides

**Type:** `table of key = action`
**Default:** `{}`

Override specific keybindings regardless of style.

```toml
[keybindings]
style = "helix"

[keybindings.overrides]
"ctrl+p" = "context_picker"
"ctrl+r" = "history_search"
"ctrl+g" = "cancel"
"ctrl+l" = "clear_screen"
"ctrl+d" = "exit"
```

**Available actions:**

| Action | Description |
|--------|-------------|
| `context_picker` | Open agent context picker |
| `history_search` | Open history search |
| `clear_screen` | Clear terminal |
| `cancel` | Cancel current input |
| `exit` | Exit shell |
| `complete` | Trigger completion |
| `accept_suggestion` | Accept agent suggestion |
| `edit_suggestion` | Edit suggestion in $EDITOR |

---

## Built-in Commands

Hash includes several built-in commands that run directly in the shell process.

### cd

Change directory. Can be disabled via `shell.disable_builtins` to use zoxide or similar.

```bash
cd ~/projects
cd -              # Previous directory
```

### history

View and search command history.

```bash
history           # Show recent 20 commands
history search <query>   # Search history
history failed    # Show failed commands
history sudo      # Show sudo commands
history asked     # Show agent interactions
```

### copy

Copy commands and output to system clipboard.

```bash
copy cmd          # Copy last command
copy out          # Copy last output
copy all          # Copy command + output
copy cmd 2        # Copy 2nd-to-last command
```

### issue

Submit issues to the Hash GitHub repository. Opens your editor with a pre-filled template including system context.

```bash
issue [TITLE]          # Open editor with template
issue --last           # Pre-fill with last command context
issue -l               # Same as --last
issue "bug title"      # Start with title
```

Requires `gh` CLI to be installed and authenticated.

### status

Show the current status of all shell subsystems.

```bash
status
```

Output includes:
- Hash version
- Prompt mode and availability
- History database path and entry count
- Learning system status and pattern count
- Agent connection status
- PTY availability
- Clipboard availability

### tips

Show helpful tips about Hash features.

```bash
tips           # Show all tips
tips off       # Disable startup hints
tips on        # Re-enable hints
```

### !!

Quick shortcut for `issue --last`. Type `!!` after a failed command to quickly submit an issue with context pre-filled.

```bash
$ some-command-that-fails
hash: command not found
$ !!
# Opens issue editor with error context
```

If the last command succeeded, you'll be prompted for confirmation before opening the issue editor.

---

## Aliases File

**File:** `~/.config/hash/aliases.toml`

Define command aliases separately from main config.

```toml
[aliases]
# Simple aliases
k = "kubectl"
g = "git"
dc = "docker compose"
tf = "terraform"

# Multi-word aliases
kgp = "kubectl get pods"
kgs = "kubectl get services"
gst = "git status"
gco = "git checkout"

# Clipboard shortcuts
cc = "copy cmd"
co = "copy out"

# Aliases can include ??
explain = "?? explain this error"
howto = "?? how do I"

# Aliases with arguments use $@ for all args, $1, $2 for positional
kex = "kubectl exec -it $1 -- /bin/sh"
```

---

## Environment Variables

These environment variables override config file settings.

| Variable | Overrides | Example |
|----------|-----------|---------|
| `HASH_CONFIG` | Config file path | `/path/to/config.toml` |
| `HASH_AGENT` | `agent.default` | `ollama` |
| `HASH_KEYBINDINGS` | `shell.keybindings` | `vim` |
| `HASH_HISTORY` | `history.path` | `/tmp/hash_history.db` |
| `HASH_CLIPBOARD_MAX_OUTPUT_SIZE` | `clipboard.max_output_size` | `unlimited` |
| `HASH_SRC` | Development source directory | `$HOME/projects/hash` |
| `EDITOR` | `shell.editor` | `nvim` |

### HASH_SRC

Path to Hash source directory. Used by `hash-rebuild` and `hash-upgrade` functions for development workflows. See the README for setup instructions.

```bash
export HASH_SRC="$HOME/projects/hash"
```

---

## Example Complete Config

```toml
# ~/.config/hash/config.toml

[shell]
editor = "hx"
keybindings = "helix"
init_commands = []
disable_builtins = []

[prompt]
mode = "starship"
dev_mode = false
dev_mode_label = "dev"

[agent]
default = "claude"
timeout = "30s"

[agent.claude]
transport = "stdio"
command = "claude"
args = ["--chat"]

[agent.ollama]
transport = "http"
url = "http://localhost:11434/api/generate"
model = "codellama:13b"

[agent.context]
include_cwd = true
include_git_branch = true
include_kube_context = true
include_env_vars = ["KUBECONFIG", "AWS_PROFILE", "NODE_ENV"]
history_count = 5

[completions]
fuzzy = true
file_icons = true
show_hidden = false

[completions.kubectl]
cache_ttl = "5m"

[completions.docker]
cache_ttl = "30s"

[history]
enabled = true
max_entries = "unlimited"
max_age = "forever"
store_agent_interactions = true
import_system_sudo = false
exclude_patterns = [
    ".*password.*",
    ".*secret.*",
    ".*token.*",
]

[errors]
auto_prompt = true
ignore_exit_codes = [1]
ignore_commands = ["grep", "diff", "test", "["]

[learning]
enabled = true
min_occurrences = 2
suggestion_threshold = 0.7
auto_apply = false

[clipboard]
max_output_size = "1MB"
buffer_size = 100
preserve_colors = false

[clipboard.keys]
copy_command = "alt+c"
copy_output = "alt+o"
copy_both = "alt+shift+c"

[prediction]
enabled = true
accept_keys = ["right", "tab"]
confidence_threshold = 0.6
path_min_count = 2
path_recency_boost_hours = 24

[ui]
context_picker_key = "ctrl+p"
completion_max_height = 10
progress_bar_delay = "2s"

[ui.colors]
suggestion = "#888888"
error = "#c27166"
success = "#8dac8b"
info = "#93bfc2"

[keybindings]
style = "helix"

[keybindings.overrides]
"ctrl+p" = "context_picker"
"ctrl+r" = "history_search"
```
