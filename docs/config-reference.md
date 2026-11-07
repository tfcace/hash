# Hash Configuration Reference

Hash reads TOML configuration from `~/.config/hash/config.toml`.

## File Locations

| File | Purpose |
|------|---------|
| `~/.config/hash/config.toml` | Main configuration |
| `~/.hashrc` | Interactive shell setup |
| `~/.hash_profile` | Login shell setup |
| `~/.local/share/hash/history.db` | Default command history database |

Set `HASH_CONFIG_DIR` to use a different config directory. Set `XDG_CONFIG_HOME`
or `XDG_DATA_HOME` to change the default config/data roots.

## Example

```toml
[shell]
editor = "hx"
dialect = "bash"
disable_builtins = ["cd"]

[input]
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

[prediction]
enabled = true
accept_keys = ["right", "tab"]
confidence_threshold = 0.6
path_recency_boost_hours = 24
```

## `[shell]`

Core shell behavior.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `editor` | string | `$EDITOR` | Terminal editor used by edit flows. |
| `dialect` | `"bash"` \| `"zsh"` | `"bash"` | Shell parser dialect for commands, `source`, `eval`, startup files, and migrated files. |
| `init_commands` | array of strings | `[]` | Commands run for every interactive shell, login or not. |
| `profile` | array of strings | `[]` | Commands run for interactive login shells. |
| `rc_commands` | array of strings | `[]` | Commands run for interactive shells. |
| `disable_builtins` | array of strings | `[]` | Builtins to let external commands handle. |

Disableable builtins include `cd`, `history`, `copy`, `issue`, `status`,
`tips`, `setup-zoxide`, `completions`, `exit`, and `quit`. `cd` is the
shell's own, as in bash (its options, `cd -`, CDPATH and messages): a
function or alias named `cd` (zoxide's `--cmd cd`, the alias `setup-zoxide`
adds) takes it over whether it is listed or not.

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
migration, and filters a zsh file that `source` or `.` reads, at startup or
later, the same way (see below).

In either mode, `typeset` and `declare` run as bash's `declare` unless they use
a flag the interpreter does not take: it takes `-a -A -f -g -n -p -r -x`, so a
line with zsh's `-U`, `-T`, `-Z` or bash's `-i`, `-l`, `-u`, any `+` flag or
`--` is skipped, as are zsh's global and suffix aliases (`alias -g`, `alias -s`).

Migrated and startup files are filtered on their syntax tree: a zsh file (one
named like zsh's: `.zshrc`, `/etc/zprofile`, `.zshrc.local`, `.zsh_aliases`,
`*.zsh`, unless its extension is `.sh` or `.bash`) is parsed as zsh, and each
statement that is skipped is replaced by `:` with its text left as a
`# [hash-compat]` comment, so every other line stays as written and keeps its
line number. A skipped statement takes all of
itself with it. `:` succeeds whatever the statement would have returned, so
the filter keeps a skip from deciding what runs next:

- When the test of an `if`, `while` or `until`, a `case` word or a `for` list
  holds a skip (anywhere in it: `if a && skipped`, `if { skipped }`,
  `if ( skipped )`, `case $(skipped) in`), the whole compound is skipped and
  none of its branches run, not even `else`.
- When the left side of `&&`, `||` or `|` is skipped, the whole list is
  skipped; when it runs but a skip in it decides its status (`a && skipped`,
  `{ a; skipped }`, a `$( )` with a skip in it), only the right side is.
- A function whose last command is skipped, or whose `return` is, returns a
  status zsh would not give it: a call to it decides nothing either, so
  `dumb() { [[ $TERM == (#i)dumb ]] }` makes `if dumb; then ...; fi` and
  `dumb && ...` skipped too. A bare `return`, or one whose status is
  not a number (`return $?`, `local r=$?; return $r`), returns the status
  of the command before it, and a command that reads `$?` reads that
  status, so does the first command of a `{ }`, `( )`, loop body or case
  arm (the command before the compound), the right of `|`, and a `case`
  word or `for` list (which then skips its compound whole): when that
  command is skipped, they decide nothing either. A status saved in a
  variable is followed only into a `return` (`st=$?; [ $st -eq 0 ]` is
  not). The filter reads the file top down, so a function that calls one
  defined only later in the file is not caught, and it takes the status
  a function is called with for kept.
- A skipped `elif` or case pattern drops itself and the branches after it;
  the ones before it stay. An arm the arm before falls into with `;&` keeps
  its body, which runs after that arm's, and when that `;&` chain ends in
  `;|`, which goes on testing the patterns after it, the arms after it stay.
- A loop whose `break` or `continue` is in a skipped statement is skipped
  whole, so it cannot run forever.
- A skipped statement that holds a `return` or an `exit`
  (`[[ zsh-only ]] && return`) may be where zsh stops reading the file
  or the function. One that is a statement of the file or of a function
  body stops it there: the rest is commented out and reported. One nested
  in a compound (a branch, a case arm, a `{ }`, a loop body, the right of
  `&&`) and holding a `return` becomes that `return`, with the skipped text
  as a comment: the file or function returns there on the paths that reach
  it, and what follows still runs on the paths that do not
  (`if (( ! $+commands[fasd] )); then source ... || return 1; fi` returns
  only when fasd is missing). A dropped `elif` that holds one becomes an
  `else` that returns, dropped case arms an arm `*)` that does. Where a
  `return` is not what zsh's would be, what follows is stopped instead: a
  guard in a pipeline or a background job (bash runs those in a subshell,
  where a return ends only the subshell), one that holds an `exit` too, and
  every guard in the zsh dialect (see the startup files below). A nested
  `exit` guard stops what follows it in the compound too: the rest of its
  `{ }` or branch, and the branches or case arms after that; a loop, or an
  `if` or `elif` test, that holds one is skipped whole (a loop would run
  its body again where zsh left it). One in a `( )` stops nothing. An
  `exit` in a function ends the shell, so a function holding a skipped one
  (or calling such a function) may end the shell: a call to it stops the
  file after the statement that holds the call, and so does a skipped
  statement that defines one. An `exec` stops nothing: the shell it starts
  (`exec tmux`, `exec zsh -l`) reads the rc file again, so what follows it
  still runs.
- A command after `;` is a statement of its own and still runs.
- A negated command (`! zmodload zsh/datetime`) is skipped like the command,
  `!` and all.

In bash mode, zsh syntax bash does not have skips its smallest statement:
expansion flags such as `${(f)x}`, glob qualifiers such as `*(N)`, `$x[1]`,
anonymous and multi-name functions. So does some zsh that bash parses but reads
differently: array indexes written as numbers, which zsh counts from 1
(`${a[1]}`, `${a[2,3]}`, `a[1]=x`), assignments to zsh's tied arrays (`fpath`,
`cdpath`, `manpath`), and `print` and `emulate`. Other zsh semantics are not
checked: computed indexes (`${a[i]}`, `${a[$i]}`, `${a[i+1]}`), `${#a}` (zsh
counts the elements, bash the length of the first), zsh's `$array` without an
index, `$var` not split at blanks, options such as `KSH_ARRAYS`, and `=cmd`. A
plain `path=(... $path)` or `path+=(...)` (words, quoted strings, `$path`, upper-case
variables) becomes the `export PATH=...` zsh makes of it. zsh's `&!` and `&|`
become `&`, zsh's test for a command on the PATH, `(( $+commands[x] ))`
or `(( ${+commands[x]} ))` (or with `!` inside), with `x` a command name
(`docker-compose`, `g++`) or a parameter (`$1`, `${name}`, unquoted: zsh
keeps quotes and blanks in the key), becomes `command -v x >/dev/null 2>&1`
(which also finds functions and builtins), so `has eza && ...` and a
plugin's `if (( ! $+commands[kubectl] )); then return; fi` decide as in
zsh. Such tests joined by `&&`, `||` and `!` in one `(( ))` become
`command -v` tests joined the same way, with `{ ...; }` where zsh's
precedence (`&&` before `||`) needs it: `(( ! $+commands[python] && !
$+commands[pyenv] ))` is `! command -v python ... && ! command -v pyenv
...`. These tests are rewritten in the zsh dialect too, whose interpreter
cannot evaluate `$+commands`. As
hash has no `noclobber`, `>!` and `>|` become `>`, and `>>!`
or `&>!` become `>>` or `&>`. A statement the bash parser still rejects is
skipped the same way. zsh's `{ cmd }` gets the `;` bash needs before `}`, and
an empty `then`, `do` or `{ }` body gets a `:`, and a case arm's `;|` becomes
bash's `;;&`.

A file the filter cannot take whole loads up to the statement that stops it,
as bash and zsh run a file up to its first syntax error: the zsh parser
rejects the statement (for example zsh's `for x (a b)` short form or a `(#i)`
case pattern), a skip would cut a heredoc body out from under its operator, or
more than 100 statements do not parse. The complete statements before it run,
and the rest of the file is commented out. Nothing in the file runs when no
complete statement comes before it, when the parser gives no position, or when
the statement starts with a closer (`fi`, `done`, `}`) whose opening line was
read as something else, since what comes before it may be the body that line
guarded. `hash migrate status` lists every skipped
line of the migrated files with its reason. A file the filter fails on is
reported as not loaded, and startup goes on. A file whose rest, or all of
it, is commented out for a statement that does not parse did not load:
startup says so in one line for the file, `hash: ~/.bashrc: not loaded: line
5: ...` (`not loaded from line 5` when the lines before it ran), and the
import summary and `hash migrate status` list it under not loaded.

A startup file (a login or interactive file, a migrated file) runs as
`source` runs a file: a `return` at its top level ends it, as in bash and
zsh, so the Debian `.bashrc` preamble (`case $- in *i*) ;; *) return;;
esac`) and `[[ -o interactive ]] || return` work. `exit` in a startup file
ends the shell with its status and `exec cmd` replaces it, as in bash,
except that an exec of a shell (`exec zsh`, `exec fish`, `exec "$SHELL"`)
in a file written for another shell, a migrated `.bashrc` say, is skipped
and listed with the skipped lines, since it would leave hash for good; an
expansion error (`${x:?}`) ends the file it is in, and an interactive shell
(`hash -ic` too) goes on with the next file while a non-interactive one
(`hash -lc`) exits with status 127. The interactive shell has
`i` in `$-` and the `interactive` option set, and so does `hash -ic`;
`hash -c` and a script have neither. In the zsh dialect a startup file with
syntax bash cannot express runs as a command line, where a `return` at its
top level is an error, which is why the filter stops at a guard there.

In the interactive shell each startup file, and each command of `profile`
and `rc_commands`, runs as a line typed at the prompt: Ctrl+C ends it, a
loop of builtins too, and startup goes on with the next one, with `$?` 130.

In the bash dialect a file that `source` or `.` reads, from a startup file,
a command line or a script (`hash -ic` too), is filtered as a migrated file
is when it is a zsh file
(`*.zsh`, `*.plugin.zsh`, `.zshrc`, ...): a migrated `.zshrc` that sources
a plugin loads the plugin's aliases and functions and skips its zsh-only
lines quietly. A startup file written for a shell (a migrated
`.bash_profile`) filters a startup file of the same shell that it sources
(the `.bashrc`) as that shell's, as it was filtered itself: an `exec zsh`
in that `.bashrc` is skipped. A script it sources (`nvm.sh`) runs as it
is. A file the filter fails on runs none of it, and hash says
so once, naming the file. A file `source` reads that does not parse runs its
statements before the syntax error, as bash's `source` does, and sets status
2. In a startup file none of it runs (a statement before the error may be a
guard that is wrong for a file written for another shell), and hash says it
in one line for the file, `hash: ~/.oh-my-zsh/oh-my-zsh.sh: not loaded:
line 24: ...`; a zsh plugin the filter cannot load all of (zinit's
`zinit.zsh`, Prezto's `init.zsh`) says so the same way. A file the filter
takes runs its statements before the one the filter stops at, and the line
says `not loaded from line 24` instead. The import summary
and `hash migrate status` list them.

Oh My Zsh is not supported: `oh-my-zsh.sh` is zsh code in a `.sh` file and
its plugin loops are zsh-only, so the bash dialect does not load it (hash
says so at each start). Source the plugins you want from your `.zshrc`, or
use `dialect = "zsh"`.

zsh's setup builtins (`setopt`, `unsetopt`, `bindkey`, `zstyle`, `compdef`,
`compinit`, `bashcompinit`, `promptinit`, `autoload`, `zmodload`, `zle`,
`add-zsh-hook`) succeed and do nothing wherever they are (after `;`, in a
function, in a sourced file), unless a function of the name is defined.
`is-at-least min [version]` compares versions as zsh's does (`5.9.1` is
past `5.9`, `5.9-test` is `5.9`); with no version it compares with
`$ZSH_VERSION`, and with none of that set it succeeds, so a plugin takes its
modern path. `shopt -s` and `shopt -u` of bash's interactive options
(`histappend`, `checkwinsize`, `cmdhist`, `lithist`, `histreedit`,
`histverify`, `autocd`, `cdspell`, `dirspell`, `direxpand`, `checkjobs`,
`checkhash`, `huponexit`, `mailwarn`, `progcomp`, `progcomp_alias`,
`hostcomplete`, `no_empty_cmd_completion`, `complete_fullquote`,
`force_fignore`, `interactive_comments`, `promptvars`) do nothing, quietly;
any other option the interpreter does not have still fails with its
message. `shopt -q` and `shopt -oq` answer whether the options are on,
quietly, as bash's (the Debian `.bashrc` asks `shopt -oq posix`); the
interactive options above are off.

### `[shell.startup_files]`

| Key | Type | Default |
|-----|------|---------|
| `login` | array of strings | `["/etc/profile", "~/.profile", "~/.hash_profile"]` |
| `interactive` | array of strings | `["~/.hashrc"]` |

Startup order is login files, `profile`, migration files, interactive files,
`rc_commands`, then `init_commands`; login files and `profile` only for a login
shell. As bash and zsh run `/etc/profile` before the user's files, the
migrated files come after the login files, so the `PATH` they set comes first
after macOS's `path_helper`. A file runs once in a startup: a migrated file
that is a login file too (`~/.profile`), or that a migrated file sources (the
`~/.bashrc` a `~/.bash_profile` sources), is not run again. A non-interactive hash (`hash -c`, `hash script.sh`, `hash -s`, a piped
stdin) follows bash: it reads the login files and runs `profile` when it is a
login shell (`hash -lc cmd`), reads the interactive files and runs
`rc_commands` with `-i` (`hash -ic cmd`), and otherwise reads none. Of the
migrated files it reads those the previous shell reads in that mode:
`~/.zshenv` always, the login ones (`~/.zprofile`, `~/.bash_profile`,
`~/.profile`) with `-l`, the interactive ones (`~/.zshrc`, `~/.bashrc`)
with `-i`, so `$SHELL -l -c env` finds the `PATH` they set. It asks no
migration question and prints no change notice, and never runs
`init_commands`. The interactive shell reads all the migrated files, login
shell or not. The startup files are parsed with
`dialect`; the command or script itself is always bash, so scripts behave the
same on every machine.

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

Each `chpwd` hook, like each of `init_commands`, runs as a line typed at the
prompt: it prints to the terminal, and Ctrl+C interrupts it (the next one runs).
A hook's status does not change `$?`, which stays the status of the line that
changed the directory.

## `[input]`

Editor-style prompt input. The editor has one built-in set of Helix-style keys; a `keybindings` key left over from older configs, in `[shell]` or `[input]`, is ignored.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `gutter` | boolean | `true` | Show the multiline input gutter. |
| `max_paste_size` | size string | `"10MB"` | Maximum paste size before truncation. |

Size strings accept plain bytes, `KB`, `MB`, `GB`, `unlimited`, and `0`.

## `[prompt]`

Prompt generation.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `mode` | `"starship"` \| `"built-in"` \| `"none"` | `"starship"` | Prompt engine. Starship falls back to the built-in prompt if unavailable; `"none"` shows a bare `❯` with no directory or colors. |
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
| `allowed_commands_scope` | `"project"` \| `"global"` \| `"session"` | `"project"` | Where persistent tool approvals are stored (project and global: a JSON file under the config dir; delete a command from it to revoke it in every shell). |
| `auto_update` | `"check"` \| `"off"` | `"check"` | Check the npm registry daily for a newer `claude-agent-acp` and offer `model update` at the prompt. Only applies to the npm-installed Claude adapter; never installs on its own. |

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
| `cobra_commands` | array of strings | `[]` | More Cobra-based tools to ask for completions, beyond the built-in kubectl, helm, gh, docker, podman, minikube and kind. Asking runs `<tool> __complete <args>`, so hash only asks tools listed here or built in, never other commands. |
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

`copy cmd|out|all [N]` counts entries from 1, the last line. Hash keeps what a line prints, up to
`max_output_size`, with one exception: a pipeline's tail at the prompt
(`ls | less`, `cat log | grep x`) writes straight to the terminal, as in any shell, so hash never sees what it
prints: `copy out` (and `copy all`, Ctrl+O in Ctrl+R) then says the command wrote straight to the terminal, and
`??` gets no last output for it. Hash does not tee the pipeline to keep it.

## `[prediction]`

Local command prediction.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | boolean | `true` | Learn successful command sequences and suggest likely next commands. |
| `accept_keys` | array of strings | `["right", "tab"]` | Reserved for prediction acceptance keys. |
| `confidence_threshold` | float | `0.6` | Minimum confidence score for command predictions. |
| `path_recency_boost_hours` | integer | `24` | Recency window for prediction scoring: a command sequence last seen this many hours ago gets half the recency score. The name is historical; it applies to command predictions. |

## Builtins

Interactive builtins:

| Command | Purpose |
|---------|---------|
| `tips` | Show common shortcuts and AI syntax. |
| `status` | Show subsystem status. |
| `history` | Inspect or search command history. |
| `copy cmd|out|all [N]` | Copy recent commands and output. |
| `issue [--last]` | Draft a GitHub issue from shell context. |
| `!!` | A line of just `!!`: `issue --last`, after asking `[y/N]` whatever the last command did. It is not bash's history expansion, which hash does not have: `sudo !!` passes a literal `!!` to sudo. |
| `completions list|reload|generate <tool>` | Manage completion plugins; `generate` asks the agent to write one. |
| `setup-zoxide` | Configure zoxide integration. |
| `source <file>` / `. <file>` | Source shell setup files. |

Compatibility no-op builtins such as `bindkey`, `setopt`, and `compdef` exist
so common zsh setup files can be filtered or sourced without failing; they
work anywhere in a line, and `is-at-least` compares versions (see Shell
Dialect). In
`shell.dialect = "zsh"`, zsh syntax and zsh init/eval lines are preserved where
possible, while unsupported runtime integration builtins remain no-ops.
