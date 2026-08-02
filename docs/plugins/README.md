# Hash plugins: production autocorrection slice (protocol v1)

Hash plugins are trusted, separately built executables. Hash discovers
`hash-plugin.toml` only below `$XDG_DATA_HOME/hash/plugins/<id>/` and each
`$XDG_DATA_DIRS/hash/plugins/<id>/`. Duplicate IDs are errors, project-local
manifests are ignored, and a fresh installation enables no plugin.

## Quickstart

```sh
hash plugin install github:roeyazroel/hash-plugins --id io.runhash.autocorrection
hash plugin inspect io.runhash.autocorrection
hash plugin enable io.runhash.autocorrection
hash plugin doctor io.runhash.autocorrection
# Start a new interactive Hash session.
hash plugin upgrade io.runhash.autocorrection
hash plugin disable io.runhash.autocorrection
hash plugin uninstall io.runhash.autocorrection
```

For a release containing multiple plugins, select one explicitly with `--id`,
or install every published bundle in deterministic ID order with `--all`:

```sh
hash plugin install --all github:roeyazroel/hash-plugins
hash plugin upgrade --all
```

`--all` leaves every newly installed plugin disabled. `upgrade --all` upgrades
only bundles managed by Hash, preserves each plugin's enabled state, and leaves
developer links unchanged. A bare install remains available for releases with
exactly one plugin; it asks you to choose when a release contains several.

The GitHub form resolves a release, selects the current OS/architecture archive,
and verifies it using the release's `SHA256SUMS`. Pin an install with
`github:owner/repository@v1.2.3` or a GitHub release URL. A plain repository
source records the unpinned origin so `upgrade` can resolve the latest release.
The archive contains the executable and `hash-plugin.toml`; users do not need
the source repository or a compiler.

Direct release artifacts are also supported when they use HTTPS and carry an
explicit digest:

```sh
hash plugin install 'https://downloads.example/plugin_1.2.3_darwin_arm64.tar.gz#sha256=<64-hex-digest>'
```

Installation is atomic and leaves the plugin disabled. Managed versions live
under `$XDG_DATA_HOME/hash/plugin-bundles/`; discovery uses an active symlink
under `$XDG_DATA_HOME/hash/plugins/`. Upgrade retains older versions for manual
rollback, while uninstall disables the plugin and removes its managed versions.
It refuses bundles created with the developer-only `link` command.

To develop against a local bundle instead:

```sh
hash plugin link /absolute/path/to/plugin-bundle
hash plugin inspect io.runhash.autocorrection
hash plugin enable io.runhash.autocorrection
hash plugin doctor io.runhash.autocorrection
# Start a new interactive Hash session.
hash plugin disable io.runhash.autocorrection
```

`link` creates a user-data symlink and never overwrites an installed bundle.
`enable` changes the ordered `[plugins].enabled` list. `doctor [id]` validates
the executable, initialize handshake, protocol version, and bounded shutdown;
without an ID it checks every enabled plugin. Plugins never start for `hash -c`
or non-interactive scripts.

## Manifest, configuration, and security

```toml
manifest_version = 1
id = "io.runhash.autocorrection"
name = "Hash Autocorrection"
version = "0.1.0"
protocol_version = 1
entrypoint = "bin/hash-autocorrection"
hooks = ["command.finished"]
commands = []

[capabilities]
context = ["history", "cwd"]
host_services = ["history.query", "completion.query"]
```

The manifest version and protocol version must be `1`. ID is a lowercase
reverse-DNS name. Name, version, and entrypoint are required; entrypoint must be
relative and stay in the bundle. Hooks, commands, context, and host services
must be arrays of unique declared names. Capabilities describe intent; they are
not an OS sandbox.

```toml
[plugins]
enabled = ["io.runhash.autocorrection"]

[plugins.settings."io.runhash.autocorrection"]
strategies = ["executable", "subcommand", "long_flag"]
history_limit = 100
max_candidates = 3
```

Adaptive prediction is opt-in and must not compete with Hash's built-in
predictor. Keep the built-in data available for rollback, but disable its
runtime surface explicitly:

```toml
[prediction]
enabled = false

[plugins]
enabled = ["io.runhash.autocorrection", "io.runhash.adaptive-prediction"]

[plugins.settings."io.runhash.adaptive-prediction"]
confidence_threshold = 0.6
learn_from_other_shells = false
shells = ["zsh", "bash", "fish"]
```

Cross-shell learning is one-time and disabled by default. A missing plugin
database is the bootstrap gate: enabling it after the database exists does
not scan history. Disable the plugin, exit Hash, and delete
`$XDG_DATA_HOME/hash/plugin-data/io.runhash.adaptive-prediction/prediction.db`
only when you intentionally want to re-import.

Enabled-list order is priority order. Strategies are ordered and unique;
allowed values are exactly `executable`, `subcommand`, and `long_flag`.
`history_limit` is 1-500 (host calls remain capped at 100) and
`max_candidates` is 1-5. Missing settings use the displayed defaults.

Enabling a plugin runs its executable with the user's operating-system
privileges and environment. Review its code and manifest first. Hash v1 does
not sandbox filesystem, process, or network access; the official correction
plugin independently promises local-only, network-free, telemetry-free
operation and never logs commands, diagnostics, history, output, or environment
values.

## Framing, lifecycle, cancellation, and failures

Stdin and stdout carry one UTF-8 JSON-RPC 2.0 object per line. Stdout is
protocol-only; diagnostics go to stderr. Hash maintains one warm process per
enabled plugin per interactive session. Request and response IDs are correlated
independently in both directions, and handlers may run concurrently.

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocol_version":1,"hash_version":"0.9.0","plugin":{"id":"io.runhash.autocorrection","version":"0.1.0"},"hooks":["command.finished"],"settings":{"max_candidates":3},"cwd":"/work","dialect":"bash"}}
```

```json
{"jsonrpc":"2.0","id":1,"result":{"protocol_version":1}}
```

Both sides must select protocol version 1. Initialization has a 500 ms
deadline. A hook canceled or expired by its parent receives:

```json
{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":6}}
```

Handlers stop work and nested calls when their context is canceled. Standard
JSON-RPC errors are supported:

```json
{"jsonrpc":"2.0","id":6,"error":{"code":-32602,"message":"invalid params"}}
```

Malformed, late, or invalid results are dropped. Three consecutive request
failures disable that plugin while healthy peers remain active. Hash restarts a
plugin once after its first unexpected exit and disables it after the second.
Shutdown sends `shutdown`, waits at most 250 ms, then terminates the child:

```json
{"jsonrpc":"2.0","id":8,"method":"shutdown","params":{}}
```

The method-specific canonical contracts live in [schemas/](schemas/).

## `command.finished`

This 150 ms request fires after Hash has committed command state and persisted
history, for successful and failed outcomes. Enabled handlers run concurrently;
the first valid non-empty result in configured priority order wins. Empty,
failed, timed-out, or disabled plugin results fall back to learned fixes and
prediction.

```json
{"jsonrpc":"2.0","id":6,"method":"command.finished","params":{"generation":42,"original_line":"git sttaus","executed_line":"git sttaus","exit_code":1,"duration_ms":18,"failure_kind":"exit_status","error_message":"","stdout_tail":"","stderr_tail":"git: 'sttaus' is not a git command","output_streams_merged":false,"cwd":"/work","dialect":"bash","canceled":false}}
```

```json
{"jsonrpc":"2.0","id":6,"result":{"corrections":["git status"]}}
```

A minimal handler decodes the request, immediately returns no candidates for
success/cancellation/interruption/signal termination, optionally calls declared
host services with `parent_request_id: 6`, then returns at most five lines.
Hash requires valid UTF-8, no controls/newlines, successful active-dialect
parsing, and exactly one eligible static executable, subcommand, or long-flag
token change. Assignments, whitespace, quoting around unchanged tokens, and
redirections must stay byte-identical. Compounds, pipelines, substitutions,
heredocs, wrappers, multiple edits, stale generations, and ambiguous repeated
tokens are rejected.

One candidate appears as a correction-owned ghost. Right fills without
submitting; Enter or Escape dismisses; printable input dismisses then edits.
Several candidates open a chooser: Up/Down and Ctrl-P/Ctrl-N select, Enter fills
and closes, and only the next ordinary Enter submits.

## Operational host services

Every plugin-originated call must be declared in the manifest and include the
active parent hook request ID. Calls inherit the parent's 150 ms deadline. Hash
rejects unknown, canceled, expired, recursive, and undeclared calls.

`host.history.query` returns at most 100 successful entries, optionally filtered
by literal command prefix and cwd:

```json
{"jsonrpc":"2.0","id":20,"method":"host.history.query","params":{"parent_request_id":6,"prefix":"git","cwd":"/work","limit":5}}
```

```json
{"jsonrpc":"2.0","id":20,"result":{"entries":[{"line":"git status","cwd":"/work","exit_code":0,"timestamp":"2026-08-01T12:00:00Z"}]}}
```

`host.completion.query` uses core-local completion only, excluding plugins,
agents, network providers, and recursion:

```json
{"jsonrpc":"2.0","id":21,"method":"host.completion.query","params":{"parent_request_id":6,"line":"git sttaus","cursor":4}}
```

```json
{"jsonrpc":"2.0","id":21,"result":{"items":[{"label":"status","insert_text":"status"}]}}
```

## `editor.suggest`

This is an operational request hook for interactive prediction plugins. Hash
calls it once at prompt creation (`trigger: "prompt"`) and again while the user
types (`trigger: "edit"`). The request is bounded by 100 ms and includes the
current generation, complete visible line, cursor, cwd, dialect, and the last
command outcome when one exists. The first valid result in enabled-list order
wins; timeout, cancellation, malformed output, unsafe text, or a stale
generation falls back to the normal core ghost.

```json
{"jsonrpc":"2.0","id":7,"method":"editor.suggest","params":{"generation":42,"trigger":"prompt","line":"","cursor":0,"cwd":"/work","dialect":"bash","previous":{"line":"git status","cwd":"/work","exit_code":0,"canceled":false}}}
```

```json
{"jsonrpc":"2.0","id":7,"result":{"text":"git pull"}}
```

`text` must be valid UTF-8, one line, free of controls, parseable by the
active dialect, at most 16 KiB, and a strict extension of the input. A prompt
prediction is ghost text: Right fills it without executing, while Enter
dismisses it and submits only visible input. Editing retains the existing
two-character minimum. Next-command providers must return an empty prompt
result after a failed, canceled, interrupted, or signaled previous command.
History providers may still answer an edit trigger after such a failure when
the user has typed a nonempty prefix.

`initialize` includes `session_kind: "interactive"`. `hash plugin doctor`
uses `session_kind: "doctor"`; diagnostic sessions must validate the handshake
without creating plugin databases or importing shell history.

## Reserved interfaces

`prompt.render`, `completion.provide`, `command.before`, `command.execute`,
`host.environment.get`, and `host.output.write` are reserved but unavailable in
this release. `session.start`, `session.stop`, `cwd.changed`, and
`history.added` remain lifecycle/observation surfaces. The schemas directory is
the canonical contract for every method; plugins should not depend on reserved
methods until a later protocol release documents them as operational.

## Packaging and troubleshooting

Ship the executable and matching manifest in a `.tar.gz` bundle, with both at
the archive root. Keep the entrypoint executable and never enable during
installation. GitHub repositories are release sources, not a registry: each
release needs one `_<os>_<arch>.tar.gz` asset plus `SHA256SUMS`. Direct artifact
URLs require an explicit SHA-256 fragment. Hash rejects HTTP, traversal,
symlinks, oversized archives, checksum mismatches, and manifest/release version
mismatches. Use `inspect` to review privileges, `doctor ID` to reproduce a
handshake/version/shutdown failure, and plugin stderr for diagnostics. An
unexpected stdout log is a malformed protocol frame. Disable for immediate
rollback; use `uninstall` only for remotely installed managed bundles.
