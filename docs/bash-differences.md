# Differences from bash

hash runs shell code with the [mvdan/sh](https://github.com/mvdan/sh) interpreter, compared here with bash 5.2.
Each line is one difference: what bash does, what hash does, and what to do about it when there is something to do.
Behavior that matches bash is not listed.

## Traps and signals

- **Subshell traps.** bash runs a subshell's own traps (`(trap 'rm "$tmp"' EXIT; ...)`) when it ends. hash sets and
  lists them but never runs them: the interpreter has no hook at a subshell's end. Only the shell's own EXIT trap
  runs, when hash exits.
- **`$?` in a signal trap.** bash leaves the last command's status in `$?` in a SIGHUP or SIGTERM trap. hash sets it
  to 128+signal, in the signal's trap and in the EXIT trap that follows.
- **A signal trap that does not exit.** bash goes on after it. A hash script still ends, dying of the signal, since
  it cannot resume the line the signal stopped. An `exit` in the trap ends the script with its status.
- **Ignored signals.** bash keeps any signal it was started with ignored. hash keeps SIGHUP and SIGINT ignored
  (`nohup`), not SIGTERM or SIGQUIT (Go keeps only the first two). An empty trap (`trap '' TERM`) ignores the signal
  in hash, but the commands hash runs do not inherit the ignore.
- **SIGQUIT between lines.** A script's QUIT trap runs once the line ended, or before the next line when the signal
  came between lines, with `$?` the last line's status (bash 5.2 does the same for a USR1 trap).
- **INT trap timing.** bash runs a script's INT trap as the command that handled Ctrl+C returns. hash runs it once the
  command's line ended; a `hash -c` program is one line, so there it runs at the end.
- **Interactive traps.** The interactive shell keeps signal traps but runs only its EXIT trap: no INT trap (as bash
  5.2), and no TERM trap where bash -i runs one (`hash -i` runs a TERM trap once the line ended).
- **A command running at a fatal signal.** bash waits for it to finish. hash sends it the same signal and kills it
  after 2 seconds.
- **An `exit` in a subshell's ERR trap** (`(trap 'exit 4' ERR; false; echo in)`) ends the subshell in bash. In hash
  the subshell goes on, and `exit` in the shell's own ERR trap ends the shell as in bash.
- **ERR trap at a failing pipeline.** bash runs it once; hash runs it twice, for the pipeline's last command and for
  the pipeline.
- **`set -e` and Ctrl+C.** bash -i with errexit on exits when Ctrl+C ends a foreground pipeline. hash's interactive
  shell ends the line with 130 and goes on.

## Background jobs

- **Jobs run inside hash.** A job (`cmd &`) is not a process of its own: hash runs its shell code itself and starts
  the commands it runs (through itself, which keeps `argv[0]` as typed and sets the signals below; a few
  milliseconds per job).
- **Process groups.** In the interactive shell each command a job starts leads a process group of its own, as with
  bash's job control: the terminal's Ctrl+C and Ctrl+\ reach only the foreground, `kill 0` in a job stays in it, and
  exit or Ctrl+D leaves the jobs running. A script's jobs share its process group, as in bash with no job control,
  and start with Ctrl+C and Ctrl+\ ignored.
- **No job control.** Ctrl+Z, `fg` and `bg` are not supported: hash never stops or continues a job.
- **A job and the terminal.** The interactive shell's job's command starts with SIGTTOU and SIGTTIN ignored, since
  nothing could continue it if the terminal stopped it: its `stty` works, and its read of the terminal (`read x
  </dev/tty`, a password prompt), or one its shell code makes, fails with an I/O error, where bash stops the job.
  A process that command starts and that resets the signals is stopped as in bash (a child of macOS's `/bin/sh`,
  bash 3.2), until `kill %N` ends it.
- **After an upgrade.** When hash's own executable was replaced since the shell started, a job's command starts as
  it is, without those ignores.
- **Stdin.** A job's stdin is `/dev/null` unless a redirect names the terminal; its output is the terminal itself.
- **Hangup at the shell's end.** When a signal ends the interactive shell, hash sends SIGHUP to the process group of
  every job's commands still running (`nohup` jobs live on), whatever the signal.
- **A script's exit.** Neither shell waits for its jobs. A command a job started keeps running; a job's shell code
  that hash runs itself is cut off, where bash's goes on as a process of its own.
- **Job ids.** `$!` names a job `g1`, `g2`, .... `kill` and `wait` take `$!`, `gN`, `%N`, `%%` or `%-`, and `wait` the
  pid `jobs -p` prints for a job that is one command, running or ended (`for p in $(jobs -p); do wait "$p"`), where
  bash's takes the pid of any process of a job; `%%` and `%-` are the jobs `jobs` marks `+` and `-` (`%-` the only one
  when one runs), among the running ones (bash's may name a job that ended and was not reported yet). `%N` is the
  shell's Nth job, where bash numbers the jobs still running, and `%name` is not taken. `kill` ends the job's shell
  code (a runaway `while :; do :; done &` too), the jobs it started, and the process groups of the commands they
  started (in a script, the commands themselves).
- **`wait -n`** waits for the first of the shell's running jobs to end, or of the jobs it names, and takes its
  status; 127 when none runs. A job that ended before the call is not one unless named, where bash's takes it until
  reported, and `-p var` is not taken.
- **`disown`** takes `-h`, `-a` and `-r`, job specs and the pids `jobs -p` prints: a disowned job leaves `jobs`, `%N`
  and a bare `wait`, and with `-h` stays listed; the shell's hangup passes both by. `$!` still names a disowned job,
  as bash's pid does.
- **`jobs`** lists the shell's running jobs, or the ones it names (`-l` adds their commands' pids; `-s` lists none). A
  job a command of it stopped is still listed as Running. `-p` names each job as `kill` takes it: by its command's pid
  when the job is one command (a function is not) and that command started within 20ms, else by its `gN` (shell code,
  a `{ ...; }` block, a pipeline), which ends the whole job, where bash lists the pid of the job's first process. A
  command other than `kill` and `wait` (`ps -p $(jobs -p)`) cannot take a `gN`.
- **Notices.** The interactive shell says `[N] pid` as a job starts (`[N] gN` when it starts no command within
  20ms) and `[N]+  Done`, `Exit N` or the signal's name before the next prompt once it ended, as bash -i; there is no
  `set -b` to say it at once.
- **Command substitutions with jobs.** hash holds `$(...)` open until each job it started that keeps the
  substitution's output open ended, as bash (`pid=$(server >log 2>&1 & echo $!)` returns at once), but tells that
  from the redirections of the job's statement and of the blocks around it, not from what its commands do later.
- **Tracing.** With `set -x`, a job traces hash's own calls (`__hash_job_started N`, `__hash_job_run N ...`) and a
  substitution that starts a job traces `__hash_subst_begin` and `__hash_subst_end`.

## Terminal and PTY

- **Foreground commands on a PTY.** In the interactive shell, a command whose stdin and output are the terminal runs
  on a PTY of its own, so hash can keep its output for `copy` and the agent. Keys typed while it runs go to it.
- **Keys a PTY command leaves unread.** As in bash, they are the next prompt's, all of them in the order typed
  (`sleep 5`, then `ls⏎` typed during it, runs `ls`; a line typed across the command's end is one line), a paste
  bigger than the PTY's input queue too. A `read` later in the same line does not see them (a later PTY command of
  the line does).
- **A long line typed during a command.** On macOS a single line over about 1 KB (the PTY's line limit), typed or
  pasted while a command runs that does not read it, loses its tail; bash loses it the same way, at the terminal's own
  limit.
- **A PTY command's parent.** It runs under a second hash process, the PTY's session leader, which hands its unread
  keys back: `$PPID` in it is that process, not the shell's `$$`, and it costs about 5 ms a command (that process's
  start, measured on an Apple Silicon Mac). When the file at hash's path is no longer the hash that runs (an upgrade
  replaced it), PTY commands run without it, and their unread keys are lost.
- **A PTY command's background children.** The command runs in a process group of its own, and a child it leaves
  running (`sh -c 'sleep 60 &'`) outlives it, as in bash, but the PTY ends with the command: the child's reads of
  it end and its writes to it fail from then on, where in bash they reach the terminal. On Linux, where a child that
  holds the PTY keeps it open, hash shows the PTY's output until it was quiet for 200 ms (2 s at the most, for a
  child that keeps writing), so such a command's prompt comes about 200 ms late, and what the child writes after
  that is lost.
- **Ctrl+C death.** A PTY command's death of Ctrl+C ends the line, as bash ends one when a job dies of it (a command
  that kills itself with SIGINT too).
- **Pipeline tails and redirected stdin.** A command whose output is the terminal but whose stdin is not (`ls |
  less`, `prog < file`) gets no PTY: its stdout and stderr are the terminal itself, as in bash. hash keeps neither:
  `copy`, the agent's context and `?? explain`, error learning, issue drafts and did-you-mean see its status but none
  of its output or error text. Passing its stderr through hash instead reorders it against its stdout.

## Errors and exit statuses

- **A command that is not found or cannot run.** The interactive shell draws its own box for it (127 or 126) where
  bash prints one line, but only when the command's stderr is the line's: with its stderr elsewhere
  (`x=$(nosuch 2>/dev/null)`, `2>&1 | ...`) it gets bash's one line there, which `2>/dev/null` silences.
- **A command that cannot start.** Status 126 with bash's message ("cannot execute binary file: Exec format error",
  "Argument list too long", "bad interpreter"); the line goes on (`./tool || fallback`). A file with no shebang that
  is not a binary runs as a `/bin/sh` script, where bash runs it with itself.
- **Expansion errors** (`${x:?msg}`, `set -u` and an unset variable) end the line with status 1 and the interactive
  shell goes on; they end a script with 127.
- **`exec`** ends the shell once its command started. One that is not there (127) or cannot start (126) leaves the
  interactive shell running, as in bash; in a script it ends the shell, one that could not start with no EXIT trap.
- **Pipelines.** In the bash dialect a pipeline's last command runs in a subshell, as bash runs it without
  `lastpipe` (`echo x | read v` leaves `v` unset); in the zsh dialect it runs in the shell itself, as zsh does.
- **A deleted working directory.** Commands run in it, as in bash, one the shell entered (`cd`, `pushd`, `popd`) and
  deleted on the same line too. A subshell whose directory cannot be entered (`(cd d && rmdir ../d && ls)`) fails its commands with 126
  and says why, where bash's subshell runs them there.
- **Messages.** Errors the interpreter reports itself (a bad option, `exit 1 2`) have no `hash:` prefix and their own
  wording; `exit 1 2` fails with 1 and the line goes on, where bash -i drops the rest of the line.

## Startup files

- See [the configuration reference](config-reference.md) for how hash reads and filters startup files.
- **A zsh file sourced in bash.** bash runs `source plugin.zsh` as bash and fails on its zsh lines. hash, in the bash
  dialect, filters a zsh file (`*.zsh`, `.zshrc`) that `source` or `.` reads as it filters a migrated one first:
  its zsh-only lines are skipped and the rest runs.
- **A sourced file that does not parse.** Both run its statements before the syntax error, except in a startup file,
  where hash runs none of it and prints one line for the file (`hash: ~/x.sh: not loaded: line 24: ...`) where bash
  prints the error. A file the compatibility filter takes (a zsh file, a startup file of the migrated shell) runs its
  statements before the one the filter stops at, and the line says so (`hash: ~/x.zsh: not loaded from line 24: ...`).
  The status is 2 where bash's is 1.
- **zsh's setup builtins.** bash has no `setopt`, `bindkey`, `zstyle`, `compdef`, `autoload`, `zmodload`, `zle`,
  `compinit` or `add-zsh-hook` (command not found). hash runs them as no-ops anywhere, and `is-at-least` as zsh's
  version check.
- **`shopt` of interactive options.** bash takes `shopt -s histappend checkwinsize` and the like. hash takes them
  quietly and does nothing; other options its interpreter has not still fail. `shopt -q` of one says it is off.
- **`$-` of the interactive shell.** Both have `i` in `$-` (`hash -i` too, so an rc file's `case $- in *i*)` guard
  passes under `hash -ic`), in every expansion of it (`${-#*e}`, `${#-}`). hash's holds only the flags it has
  (`aefinux`), none of bash's `h`, `m`, `B`, `H`, `s`. `[[ -o interactive ]]` holds there, as in zsh (bash has no such
  option). `set -x` traces each `$-` in a function as `${__hash_flags[$-]}`, the read-only array hash serves it
  from; `declare -f` prints the function as it was written.
