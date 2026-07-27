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

## Generating plugins with the agent

The fastest way to write a plugin is to let the shell's agent do it:

```
completions generate kubectl
completions generate terraform "complete workspace names too"
```

`completions generate <tool>` captures the tool's `--help` output, asks the
configured `??` agent to draft a spec in the format below, and shows you the
TOML. From there it is a conversation:

```
[a]ccept  [r]evise <what to change>  [q]uit: r also complete namespaces
```

Revise as many times as you like; each round sends your instruction and the
current spec back to the agent. A draft that fails validation is just another
round: you see the error and can steer the fix rather than the command giving
up. Accepting writes the spec to `~/.config/hash/completions/<tool>.toml`,
activates it immediately (no restart), and ends the conversation.

A generated plugin is a normal file you own from then on. To change it later,
edit it and run `completions reload`, re-run `completions generate <tool>` to
replace it, or delete it.

Related subcommands:

```
completions list      # show registered plugin handlers (built-in and user)
completions reload    # re-read user specs after editing them by hand
```

## Where plugins live

User plugins are TOML files in:

```
~/.config/hash/completions/*.toml
```

Files are loaded in filename order at shell startup (and on `completions
reload`). A file that fails to parse prints a warning and is skipped; the
rest still load. Plugins can be turned off entirely with
`completions.plugins_enabled = false` in `config.toml`.

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
| `value_flags` | array of strings | no | Global flags that consume the next word as their value, e.g. `["--context", "-H"]` for docker. Without this, `docker --context remote rm <TAB>` would read `remote` as the subcommand and match no rule. `--flag=value` needs no declaration. |
| `disabled` | boolean | no | Remove completions for `commands`, including built-in ones. |

Unknown keys anywhere in a spec are validation errors, so a misspelled field
fails loudly instead of silently falling back to its default.

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
| `cache_ttl` | duration string | `"2s"` | Reuse output between keystrokes for this long. `"0s"` disables reuse entirely: every completion re-queries the source. Use it when the tool's own commands change the answer (docker, systemctl), so completion never shows pre-command state. |

Each output line becomes one completion item. Empty values and duplicates
are dropped, and candidates are filtered by what has already been typed
(plus fuzzy matching when `completions.fuzzy` is on).

## Behavior notes

- **Priority**: plugins run first, ahead of tool-native (Cobra `__complete`)
  completion. A matched rule is curated (per-subcommand sources, descriptions)
  where `__complete` returns bare names, so the plugin answer wins whenever a
  rule matches. Input no rule matches falls through to the other tiers, and a
  user plugin can override any built-in handler (`docker`, `ssh`, `kill`).
- **A matched rule owns the argument**: when a rule matches and its source
  answers, an empty candidate list means "no matches" — completion does not
  fall through and offer filenames for something like a container name.
- **Failure is silent**: if the source command fails (tool not installed,
  docker daemon not running), the plugin produces no items and completion
  falls through to the next tier. A failure is remembered for a few seconds
  so a broken source is not relaunched on every TAB. Slow sources are cut
  off by `timeout`.
- **Isolation**: sources run in their own session with stdin from
  `/dev/null`, and are killed as a group on cancellation. They should be
  read-only, fast, and non-interactive.
- **Caching**: output is cached per `exec` argv *and working directory* for
  `cache_ttl`, so holding TAB or typing quickly does not hammer the source
  command, and directory-sensitive sources (`terraform workspace list`)
  never leak results across a `cd`. With `cache_ttl = "0s"` nothing is
  reused (beyond a ~1s handoff to the notice-refresh described below), for
  sources whose answer changes when the user runs the command itself.
- **Cold TAB does not block**: a source with nothing cached gets a short
  grace period (40ms) to answer. Past that, the shell shows a dim
  "fetching completions..." notice while the source keeps running in the
  background, and the menu opens by itself as soon as the data lands — no
  second TAB needed. If the source fails instead, the notice clears and
  completion falls back to the next tier.
- **Stale results are served while refreshing**: once `cache_ttl` expires,
  the previous output is still offered (for up to a minute) while a refresh
  runs in the background. This keeps TAB instant for sources that take
  longer than the completion deadline, so `cache_ttl` controls how fresh
  the data is, not whether completion feels responsive.

Set `timeout` generously. It bounds a background process, not the TAB you
are waiting on, and a source killed by its timeout caches nothing, so it
fails identically on every attempt. The built-in `docker` spec uses
`timeout = "3s"` because `docker ps` regularly takes several hundred
milliseconds against a real daemon, and `cache_ttl = "0s"` because docker
commands change what the sources return: `docker stop web` must move `web`
from the `stop <TAB>` list to the `start <TAB>` list immediately, not when
a cache expires.

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
