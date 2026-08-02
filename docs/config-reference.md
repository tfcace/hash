# Hash Configuration Reference

Hash reads TOML configuration from `~/.config/hash/config.toml`.

## File Locations

| File | Purpose |
|------|---------|
| `~/.config/hash/config.toml` | Main configuration |
| `~/.hashrc` | Interactive shell setup |
| `~/.hash_profile` | Login shell setup |
| `~/.local/share/hash/history.db` | Default command history database |
| `~/.local/share/hash/learning.db` | Adaptive learned-fix database |

Set `HASH_CONFIG_DIR` to use a different config directory. Set `XDG_CONFIG_HOME`
or `XDG_DATA_HOME` to change the default config/data roots.

## Example

```toml
[shell]
editor = "hx"
dialect = "bash"
disable_builtins = ["cd"]

[input]
keybindings = "helix"
gutter = true
max_paste_size = "10MB"

[prompt]
mode = "starship"

[agent]
default = "claude"
timeout = "120s"
allowed_commands_scope = "project"

[agent.claude]
transport = "stdio"
command = "claude-agent-acp"

[agent.ollama]
transport = "http"
url = "http://localhost:11434/api/generate"
model = "codellama:13b"

[history]
enabled = true
path = "~/.local/share/hash/history.db"

[completions]
fuzzy = true
file_icons = true
cobra_enabled = true
mask_sensitive_env = true
plugins_enabled = true

[clipboard]
max_output_size = "1MB"
buffer_size = 100
preserve_colors = false

[learning]
enabled = true

[prediction]
enabled = true
accept_keys = ["right", "tab"]
confidence_threshold = 0.6
path_min_count = 2
path_recency_boost_hours = 24

[plugins]
enabled = ["io.example.my-plugin"]

[plugins.settings."io.example.my-plugin"]
strategy = "history"
```

## `[plugins]`

External plugins are discovered only from XDG user/system data locations and
are disabled by default. `enabled` is an ordered array: its order sets
single-winner priority and aggregate contribution order. Per-plugin settings
are forwarded unchanged to the plugin's initialization request.

```toml
[plugins]
enabled = ["io.example.my-plugin"]

[plugins.settings."io.example.my-plugin"]
minimum_length = 2
```

Use `hash plugin install`, `upgrade`, `uninstall`, `list`, `inspect`, `link`,
`enable`, `disable`, and `doctor` to manage bundles. Install accepts a GitHub
release repository or a checksummed HTTPS artifact and never enables the plugin
automatically. Plugin executables are trusted local programs with your user
privileges; capability declarations are not a sandbox. See the complete
[plugin developer guide](plugins/README.md).

For a multi-plugin release, use `install --id <plugin-id> <source>` or
`install --all <source>`; a bare install asks you to choose. Use
`upgrade --all [source]` to upgrade every Hash-managed bundle while preserving
enabled state. Developer links are left unchanged.

## `[shell]`

Core shell behavior.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `editor` | string | `$EDITOR` | Terminal editor used by edit flows. |
| `keybindings` | `"emacs"` \| `"vim"` \| `"helix"` | `"emacs"` | Readline compatibility mode. |
| `dialect` | `"bash"` \| `"zsh"` | `"bash"` | Shell parser dialect for commands, `source`, `eval`, startup files, and migrated files. |
| `init_commands` | array of strings | `[]` | Commands run for every shell mode. |
| `profile` | array of strings | `[]` | Commands run for login shells. |
| `rc_commands` | array of strings | `[]` | Commands run for interactive shells. |
| `disable_builtins` | array of strings | `[]` | Builtins to let external commands handle. |

Disableable builtins include `cd`, `history`, `copy`, `issue`, `status`,
`tips`, `setup-zoxide`, `completions`, `source`, `exit`, and `quit`.

### Shell Dialect

Hash uses the `mvdan.cc/sh` interpreter. The default dialect is bash for
stability and backwards compatibility:

```toml
[shell]
dialect = "bash"
```

To opt into zsh parsing:

```toml
[shell]
dialect = "zsh"
```

In zsh mode, normal commands, `source`, `eval`, configured startup files, and
migrated zsh files are parsed with `syntax.LangZsh`. Upstream zsh support is
experimental and incomplete, so Hash still treats shell/editor integration
builtins such as `bindkey`, `setopt`, `compdef`, and `zstyle` as compatibility
no-ops. Bash mode continues to filter common zsh-only init/eval lines during
migration.

### `[shell.startup_files]`

| Key | Type | Default |
|-----|------|---------|
| `login` | array of strings | `["/etc/profile", "~/.profile", "~/.hash_profile"]` |
| `interactive` | array of strings | `["~/.hashrc"]` |

Startup order is migration files, login files, `profile`, interactive files,
`rc_commands`, then `init_commands`.

The defaults stay Hash-specific even in zsh mode. To source zsh startup files
directly, configure them explicitly:

```toml
[shell]
dialect = "zsh"

[shell.startup_files]
login = ["/etc/zprofile", "~/.zprofile", "~/.hash_profile"]
interactive = ["~/.zshrc", "~/.hashrc"]
```

If you rely on `~/.zshenv`, add it carefully to the startup lists you need;
Hash does not currently have a separate “all shell invocations” startup bucket.

### `[shell.hooks]`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `chpwd` | array of strings | `[]` | Commands run after the working directory changes. |

## `[input]`

Editor-style prompt input.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `keybindings` | `"helix"` \| `"emacs"` \| `"vim"` | `"helix"` | Built-in editor keybinding mode. |
| `gutter` | boolean | `true` | Show the multiline input gutter. |
| `max_paste_size` | size string | `"10MB"` | Maximum paste size before truncation. |

Size strings accept plain bytes, `KB`, `MB`, `GB`, `unlimited`, and `0`.

## `[prompt]`

Prompt generation.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `mode` | `"starship"` \| `"built-in"` \| `"none"` | `"starship"` | Prompt engine. Starship falls back to the built-in prompt if unavailable. |
| `starship_path` | string | auto-detected | Explicit path to the Starship binary. |
| `alignment` | `"left"` \| `"right"` | `"left"` | Prompt alignment hint for the built-in prompt. |

The built-in prompt is intentionally minimal in 0.6.x; advanced prompt theme and
format tables are not currently supported.

## `[agent]`

AI agent selection and shared behavior.

For Claude over ACP, install the current adapter with
`npm install -g @agentclientprotocol/claude-agent-acp` and use
`command = "claude-agent-acp"`. Gemini CLI (`command = "gemini"`,
`args = ["--experimental-acp"]`) and Cursor CLI (`command = "agent acp"`, after
`agent login`) speak ACP too. See the [agents guide](https://runhash.dev/docs/agents).

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `default` | string | `"claude-agent-acp"` | Name of the selected `[agent.<name>]`, or a label for flat config. |
| `timeout` | duration string | `"120s"` | Agent request timeout. |
| `allowed_commands_scope` | `"project"` \| `"global"` \| `"session"` | `"project"` | Where persistent tool approvals are stored. |

Hash supports both flat agent config and named agents.

Flat config:

```toml
[agent]
transport = "stdio"
command = "claude-agent-acp"
args = []
```

Named agents:

```toml
[agent]
default = "ollama"

[agent.ollama]
transport = "http"
url = "http://localhost:11434/api/generate"
model = "codellama:13b"
headers = { Authorization = "Bearer token" }
```

### Agent Transport Keys

| Key | Type | Used by | Description |
|-----|------|---------|-------------|
| `transport` | `"stdio"` \| `"http"` | all | Agent transport. Unset means ACP/stdio when `command` is present. |
| `command` | string | stdio | Command to launch. |
| `args` | array of strings | stdio | Arguments for `command`. |
| `url` | string | http | HTTP endpoint, for example Ollama `/api/generate`. |
| `model` | string | http | Model name sent to the HTTP agent. |
| `headers` | table of strings | http | Optional request headers. |
| `timeout` | duration string | named agents | Overrides `[agent].timeout` for that agent. |

## `[history]`

Command history storage.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | boolean | `true` | Enable SQLite-backed command history. |
| `path` | string | `"~/.local/share/hash/history.db"` | History database path. |

Retention limits are not implemented in 0.6.x.

## `[completions]`

Tab completion behavior.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `fuzzy` | boolean | `true` | Enable fuzzy filtering. |
| `file_icons` | boolean | `true` | Show file type icons when the terminal font supports them. |
| `cobra_enabled` | boolean | `true` | Enable Cobra `__complete` integration. |
| `mask_sensitive_env` | boolean | `true` | Mask sensitive environment variable values in previews. |
| `plugins_enabled` | boolean | `true` | Declarative completion plugins: built-in specs (docker) plus user specs from `~/.config/hash/completions/*.toml`. See [completion-plugins.md](completion-plugins.md). |

Per-tool completion caching is configured in the plugin specs themselves,
not here: each `[rules.source]` has a `cache_ttl` (including `"0s"` for no
reuse). See [completion-plugins.md](completion-plugins.md).

## `[clipboard]`

In-memory command/output capture for copy shortcuts and context.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `max_output_size` | size string | `"1MB"` | Captured output limit per command. Use `unlimited` or `0`. |
| `buffer_size` | integer | `100` | Number of command/output entries to keep. |
| `preserve_colors` | boolean | `false` | Reserved; copied output is currently plain text. |

`HASH_CLIPBOARD_MAX_OUTPUT_SIZE` overrides `clipboard.max_output_size`.

## `[learning]`

Adaptive post-failure fix learning.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | boolean | `true` | Record successful commands following failures and offer matching learned fixes. |

Set `enabled = false` to stop opening, updating, and consulting
`learning.db`. Existing learned data is preserved in case learning is enabled
again. Restart Hash after changing this setting.

## `[prediction]`

Local command and path prediction.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | boolean | `true` | Learn successful command sequences and suggest likely next commands. |
| `accept_keys` | array of strings | `["right", "tab"]` | Reserved for prediction acceptance keys. |
| `confidence_threshold` | float | `0.6` | Minimum confidence score for command predictions. |
| `path_min_count` | integer | `2` | Minimum uses before a path becomes eligible for suggestions. |
| `path_recency_boost_hours` | integer | `24` | Recency window for prediction scoring. |

To use `io.runhash.adaptive-prediction`, set the built-in `enabled = false`
while retaining its database for rollback, then enable the external plugin
through `[plugins]`. The external plugin has independent settings and data.

## Builtins

Interactive builtins:

| Command | Purpose |
|---------|---------|
| `tips` | Show common shortcuts and AI syntax. |
| `status` | Show subsystem status. |
| `history` | Inspect or search command history. |
| `copy cmd|out|all [N]` | Copy recent commands and output. |
| `issue [--last]` | Draft a GitHub issue from shell context. |
| `completions list|reload|generate <tool>` | Manage completion plugins; `generate` asks the agent to write one. |
| `setup-zoxide` | Configure zoxide integration. |
| `source <file>` / `. <file>` | Source shell setup files. |

Compatibility no-op builtins such as `bindkey`, `setopt`, and `compdef` exist
so common zsh setup files can be filtered or sourced without failing. In
`shell.dialect = "zsh"`, zsh syntax and zsh init/eval lines are preserved where
possible, while unsupported runtime integration builtins remain no-ops.
