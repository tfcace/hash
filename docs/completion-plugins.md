# Completion Plugins

Hash tab completion is extensible with declarative **completion plugins**:
small TOML files that teach the shell where completion candidates for a
command come from. No shell scripting is required — a plugin declares a
command to run and how to parse its output into completion items.

Hash ships with a built-in plugin for `docker` (containers, images, volumes,
networks), written in the exact same format described here. For example:

```
docker rm <TAB>
web-server    abc123def456  nginx:latest  (Up 2 hours)
postgres      789aaa000bbb  postgres:16   (Exited (0) 3 days ago)
```

## Where plugins live

User plugins are TOML files in:

```
~/.config/hash/completions/*.toml
```

Files are loaded in filename order at shell startup. A file that fails to
parse prints a warning and is skipped; the rest still load. Plugins can be
turned off entirely with `completions.plugins_enabled = false` in
`config.toml`.

A user plugin that declares the same command as a built-in plugin **replaces**
the built-in handler for that command. To remove a built-in without providing
a replacement, ship a disabled spec:

```toml
# ~/.config/hash/completions/no-docker.toml
[plugin]
name = "docker"
commands = ["docker"]
disabled = true
```

## Spec format

```toml
# ~/.config/hash/completions/kubectl.toml
[plugin]
name = "kubectl"
description = "Pod names for kubectl"
commands = ["kubectl", "k"]

[[rules]]
subcommands = ["delete pod", "describe pod", "logs"]
[rules.source]
exec = ["kubectl", "get", "pods", "-o", "custom-columns=NAME:.metadata.name,STATUS:.status.phase", "--no-headers"]
value_column = 1
description_column = 2
timeout = "800ms"
cache_ttl = "5s"
```

### `[plugin]`

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `name` | string | yes | Plugin identifier used in warnings. |
| `description` | string | no | Human-readable summary. |
| `commands` | array of strings | yes | Command names this plugin completes (no spaces). |
| `disabled` | boolean | no | Remove completions for `commands`, including built-in ones. |

### `[[rules]]`

Rules are tried in order; the first rule whose subcommand path matches the
line wins. Flags on the line (words starting with `-`) are ignored when
matching, so `docker rm -f <TAB>` still matches the `rm` rule. Plugins never
complete flags themselves — typing `-<TAB>` falls through to other
completers.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `subcommands` | array of strings | `[]` | Subcommand paths this rule applies to, e.g. `["rm", "container rm"]`. Empty matches any arguments of the command. |
| `max_args` | integer | `0` | Maximum positional arguments after the subcommand that this rule completes (`0` = unlimited). Use `1` for `docker run IMAGE cmd...`-style commands where only the first positional should be completed. |

### `[rules.source]`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `exec` | array of strings | required | Command to run for candidates. Executed directly (argv), with no shell interpretation. Wrap with `["sh", "-c", "..."]` if you need pipes. |
| `delimiter` | string | whitespace | Column separator for each output line. |
| `value_column` | integer | `1` | 1-based column inserted into the command line. |
| `description_column` | integer | `0` | 1-based column shown next to the value in the menu (`0` = none). |
| `timeout` | duration string | `"500ms"` | Kill the source if it runs longer. |
| `cache_ttl` | duration string | `"2s"` | Reuse output between keystrokes for this long. |

Each output line becomes one completion item. Empty values and duplicates
are dropped, and candidates are filtered by what has already been typed
(plus fuzzy matching when `completions.fuzzy` is on).

## Behavior notes

- **Priority**: plugins run after tool-native (Cobra) and VCS completion but
  before the built-in semantic handlers and filesystem fallback, so a user
  plugin can override handlers like `ssh` or `kill`.
- **Failure is silent**: if the source command fails (tool not installed,
  docker daemon not running), the plugin produces no items and completion
  falls through to the next tier. Slow sources are cut off by `timeout` and
  by the completion UI's own deadline.
- **Isolation**: sources run in their own session with stdin from
  `/dev/null`, and are killed as a group on cancellation. They should be
  read-only, fast, and non-interactive.
- **Caching**: output is cached per `exec` argv for `cache_ttl`, so holding
  TAB or typing quickly does not hammer the source command.

## More examples

Systemd units:

```toml
[plugin]
name = "systemctl"
commands = ["systemctl"]

[[rules]]
subcommands = ["start", "stop", "restart", "status", "enable", "disable"]
[rules.source]
exec = ["systemctl", "list-units", "--all", "--plain", "--no-legend", "--no-pager"]
value_column = 1
description_column = 4
cache_ttl = "10s"
```

Virtual environments for a custom tool, using a shell pipeline:

```toml
[plugin]
name = "workon"
commands = ["workon"]

[[rules]]
[rules.source]
exec = ["sh", "-c", "ls -1 ~/.virtualenvs"]
cache_ttl = "30s"
```
