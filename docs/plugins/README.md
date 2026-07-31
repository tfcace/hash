# Hash plugins (protocol v1)

Hash plugins are trusted, separately-built executables. Hash discovers their
`hash-plugin.toml` files from `$XDG_DATA_HOME/hash/plugins/<id>/` and each
`$XDG_DATA_DIRS/hash/plugins/<id>/`; it never searches a project directory.
Duplicate IDs stop discovery. A fresh installation has an empty enabled list.

## Quickstart

Build a plugin bundle, then link and explicitly enable it:

```sh
hash plugin link /absolute/path/to/my-plugin
hash plugin inspect io.example.my-plugin
hash plugin enable io.example.my-plugin
hash plugin doctor
# start a new interactive Hash session
hash plugin disable io.example.my-plugin
```

`link` creates one user-data symlink and refuses to overwrite an existing
bundle. `enable` changes only the ordered `[plugins].enabled` list in the user
configuration. `doctor` checks enabled bundles and entrypoint executability.

## Manifest and configuration

```toml
manifest_version = 1
id = "io.example.my-plugin"       # required, globally unique
name = "My Plugin"                # required display name
version = "0.1.0"                 # required plugin version
protocol_version = 1               # must equal the Hash protocol version
entrypoint = "bin/my-plugin"      # required, relative and cannot escape bundle
hooks = ["editor.suggest"]        # methods this executable implements
commands = ["my-command"]         # declared top-level commands

[capabilities]
context = ["editor", "history", "cwd"]
host_services = ["history.query", "completion.query"]
```

`manifest_version`, `protocol_version`, `id`, `name`, `version`, and
`entrypoint` are required. Absolute and parent-directory entrypoints are
rejected. `hooks` and `commands` are declarations, not permissions.

```toml
[plugins]
# This exact order is priority order for single-winner hooks.
enabled = ["io.example.my-plugin"]

[plugins.settings."io.example.my-plugin"]
strategy = "history"
minimum_length = 2
```

Settings are untyped TOML data passed unchanged in `initialize`. Plugin
executables receive your operating-system user privileges. The capability list
is useful for review in `hash plugin inspect`, but is **not an OS sandbox**.

## Lifecycle, concurrency, and failures

Hash starts one warm process per enabled plugin after interactive startup files
finish; it never starts plugins for `hash -c` or non-interactive scripts.
Stdout is protocol-only and stderr is diagnostic-only. Requests are multiplexed
over newline-delimited JSON-RPC 2.0. A request is canceled with
`$/cancelRequest` when its editor generation becomes stale or its deadline
expires. Responses that are malformed, late, or fail validation are dropped.

Single-winner hooks run concurrently conceptually but select the first valid
result in enabled-list priority. Prompt and completion contributions aggregate
in enabled-list order. Hash isolates plugin failure; v1 allows one restart and
disables a process after its second exit or three consecutive request failures.
Shutdown sends `shutdown`, allows 250 ms, then terminates the child.

| Operation | Deadline | Fallback |
|---|---:|---|
| `initialize` | 500 ms | plugin unavailable |
| `prompt.render` | 50 ms | omit contribution |
| `editor.suggest` | 100 ms | no ghost text |
| `completion.provide` | 150 ms | core completion only |
| `command.before`, `command.finished` | 150 ms | original execution / no diagnostic |
| notifications | non-blocking; canceled after 500 ms | ignored |

## Framing, initialization, errors, and shutdown

The canonical envelope schema is [protocol-v1.schema.json](protocol-v1.schema.json).
Every line is one UTF-8 JSON object. A successful handshake is:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocol_version":1,"plugin":{"id":"io.example.my-plugin","version":"0.1.0"},"settings":{"strategy":"history"}}}
{"jsonrpc":"2.0","id":1,"result":{"protocol_version":1}}
```

Both sides must return protocol version `1`; no compatible version means Hash
closes the process. For a canceled request Hash writes:

```json
{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":7}}
```

Return standard JSON-RPC errors, for example
`{"jsonrpc":"2.0","id":7,"error":{"code":-32602,"message":"invalid line"}}`.
On shutdown Hash sends `{"jsonrpc":"2.0","id":8,"method":"shutdown","params":{}}`;
reply normally and exit.

## Hooks

All handler snippets use pseudocode: `reply(id, value)` writes a one-line
JSON-RPC response; notifications have no response. Values are illustrative but
complete JSON messages.

| Method | Kind, time, order, validation and fallback | Minimal handler and terminal effect |
|---|---|---|
| `session.start` | Notification after startup; non-blocking; enabled order; failure ignored. Request: `{"jsonrpc":"2.0","method":"session.start","params":{"cwd":"/work","dialect":"bash"}}`. | `if method=="session.start": warm_cache()`; no visible terminal change. |
| `session.stop` | Notification before shutdown; non-blocking; failure ignored. Request: `{"jsonrpc":"2.0","method":"session.stop","params":{"cwd":"/work"}}`. | `flush_state()`; no visible change. |
| `cwd.changed` | Notification after Hash commits `cd`; non-blocking; failure ignored. Request: `{"jsonrpc":"2.0","method":"cwd.changed","params":{"cwd":"/tmp"}}`. | `cache.clear()`; no visible change. |
| `prompt.render` | Request, 50 ms, aggregate in enabled order. Request: `{"jsonrpc":"2.0","id":2,"method":"prompt.render","params":{"cwd":"/work","exit_code":0}}`; response: `{"jsonrpc":"2.0","id":2,"result":{"segments":[{"text":"dev","style":"muted","placement":"prefix"}]}}`. ANSI, controls, newlines, unapproved style or placement are rejected; omitted on failure. | `reply(id,{segments:[{text:"dev",style:"muted",placement:"prefix"}]})`; a typed prefix appears before the core prompt. |
| `editor.suggest` | Request, 100 ms; priority winner, canceled on newer editor generation. Request: `{"jsonrpc":"2.0","id":3,"method":"editor.suggest","params":{"line":"git","cwd":"/work"}}`; response: `{"jsonrpc":"2.0","id":3,"result":{"text":"git status"}}`. Must be valid UTF-8 strict end-of-buffer extension; otherwise no ghost. | `reply(id,{text:history_prefix(line)})`; `git` displays a dim ` status`; Right accepts it. |
| `completion.provide` | Request, 150 ms; aggregate enabled order. Request: `{"jsonrpc":"2.0","id":4,"method":"completion.provide","params":{"line":"git ch","cursor":6}}`; response: `{"jsonrpc":"2.0","id":4,"result":{"items":[{"label":"checkout","insert_text":"checkout","replace_start":4,"replace_end":6}]}}`. Hash bounds spans/items, deduplicates, and excludes plugin/agent/network recursion; invalid items are dropped. | `reply(id,{items:[...]})`; Tab menu merges it with core candidates. |
| `command.before` | Request, 150 ms, priority winner once per submission. Request: `{"jsonrpc":"2.0","id":5,"method":"command.before","params":{"line":"gti status","cwd":"/work"}}`; response: `{"jsonrpc":"2.0","id":5,"result":{"line":"git status","message":"fix typo"}}`. One transformed line only; Hash validates it and does not rerun hooks. | `reply(id,{line:"git status",message:"fix typo"})`; Yes executes transformed, No original, Escape cancels. |
| `command.finished` | Request, 150 ms after execution. Request: `{"jsonrpc":"2.0","id":6,"method":"command.finished","params":{"original_line":"git sttaus","executed_line":"git sttaus","exit_code":1,"stdout_tail":"","stderr_tail":"git: ...","cwd":"/work","dialect":"bash"}}`; response: `{"jsonrpc":"2.0","id":6,"result":{"corrections":["git status"]}}`. Hash bounds output and drops invalid corrections. | `reply(id,{corrections:["git status"]})`; one correction is a protected Right-accept ghost; several open selection, and a second Enter executes. Never auto-executes. |
| `history.added` | Notification after successful persistence; request: `{"jsonrpc":"2.0","method":"history.added","params":{"line":"git status","exit_code":0,"duration_ms":12,"cwd":"/work"}}`. Observe-only, non-blocking, ignored on failure. | `observe(params)`; no visible change. |
| `command.execute` | Request for a declared top-level command; streamed through `host.output.write`. Request: `{"jsonrpc":"2.0","id":7,"method":"command.execute","params":{"command":"my-command","args":["x"]}}`; response: `{"jsonrpc":"2.0","id":7,"result":{"exit_code":0}}`. Hash supports redirection/pipes, but rejects piped stdin and PTY takeover. | `write("stdout","done\\n"); reply(id,{exit_code:0})`; `my-command x > out` writes `done` to `out`. |

## Host services

Plugins issue these ordinary JSON-RPC requests to Hash. Calls are correlated by
ID, limited to their declared informational capability, and honor their parent
hook deadline. They never provide direct database, PTY, or arbitrary shell
state access.

| Service | Complete request / response | Handler use |
|---|---|---|
| `host.history.query` | `{"jsonrpc":"2.0","id":20,"method":"host.history.query","params":{"prefix":"git","limit":5}}` → `{"jsonrpc":"2.0","id":20,"result":{"entries":[{"line":"git status","exit_code":0}]}}` | `entries = host.call("host.history.query", {prefix: line, limit: 5})` |
| `host.completion.query` | `{"jsonrpc":"2.0","id":21,"method":"host.completion.query","params":{"line":"git ch","cursor":6}}` → `{"jsonrpc":"2.0","id":21,"result":{"items":[{"label":"checkout","insert_text":"checkout"}]}}` | `core = host.call("host.completion.query", request)`; it excludes plugins, agents, network, and recursion. |
| `host.environment.get` | `{"jsonrpc":"2.0","id":22,"method":"host.environment.get","params":{"names":["PATH","HOME"]}}` → `{"jsonrpc":"2.0","id":22,"result":{"values":{"PATH":"/bin","HOME":"/home/u"}}}` | `env = host.call("host.environment.get", {names:["PATH"]})` after shell exports. |
| `host.output.write` | `{"jsonrpc":"2.0","id":23,"method":"host.output.write","params":{"stream":"stdout","text":"done\\n"}}` → `{"jsonrpc":"2.0","id":23,"result":{}}` | `host.call("host.output.write", {stream:"stdout",text:"done\\n"})` for command streaming. |

## Packaging and troubleshooting

Ship a bundle directory containing the manifest and executable. Keep the
entrypoint executable, use a stable reverse-DNS ID, and declare only protocol
v1. Hash does not download plugins, install a registry, or enable them for
you. Use `hash plugin inspect ID` to review the executable and requested
capability metadata, `hash plugin doctor` for installation problems, and
stderr from the plugin for diagnostics. Malformed stdout is a protocol failure:
write logs only to stderr.
