package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/tfcace/hash/internal/compat"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/tilde"
	"github.com/tfcace/hash/internal/trace"
)

// StepEnd is how a startup step ended: a startup file, a migrated file or
// a command of the config. It decides whether startup goes on, the same
// for the interactive shell and for hash -lc and -ic (Startup).
type StepEnd int

const (
	// StepDone: the step ran, to its end or not (a return at its top
	// level, Ctrl+C, an expansion error in an interactive shell), or did
	// not run (a missing file; one that could not be read or filtered,
	// which it said): the next step runs.
	StepDone StepEnd = iota
	// StepExited: the step ended the shell, as in bash: exit or exec in
	// it, or a fatal error (an expansion error) in a non-interactive
	// shell. No step runs after it; the shell exits with the status.
	StepExited
	// StepCanceled: the shell is ending (a fatal signal, hash ending): no
	// step runs after it.
	StepCanceled
)

// StepOutcome is how a startup step ended, with the shell's exit status
// for StepExited.
type StepOutcome struct {
	End    StepEnd
	Status int
}

// Startup runs a shell's startup files and the commands of its config
// before its first prompt or command (Run). The interactive shell and a
// non-interactive hash that reads startup files (hash -lc, -ic) run it
// alike.
type Startup struct {
	exec *executor.Executor
	cfg  *config.Config
	// login: it reads the login files and runs the profile commands.
	login bool
	// interactive: it reads the interactive files and runs rc_commands, and
	// a fatal error ends only the file it is in.
	interactive bool
	// stdout, stderr are where its files and commands write, and it says
	// what went wrong.
	stdout, stderr io.Writer
	// ran is the files run so far, as startup files or by source in them:
	// a file runs once in a startup, as ~/.profile, a login file the
	// migration names too, or the ~/.bashrc a migrated ~/.bash_profile
	// sources.
	ran []os.FileInfo
	// line runs a step's text (run) on ctx, the startup's: the interactive
	// shell runs it as a line typed at its prompt runs (Shell.onLine), so
	// Ctrl+C ends the step's work and the next step runs. As it is
	// otherwise (hash -lc, -ic: SIGINT ends the script).
	line func(ctx context.Context, run lineRun) (*executor.Result, error)
}

// lineRun runs a line's text in the executor on ctx.
type lineRun func(ctx context.Context) (*executor.Result, error)

// asItIs runs run on ctx, as it is: a step of a non-interactive startup.
func asItIs(ctx context.Context, run lineRun) (*executor.Result, error) { return run(ctx) }

// NewStartup is the startup of a shell that runs its commands with e,
// configured by cfg: a login shell, an interactive one, both or neither.
func NewStartup(e *executor.Executor, cfg *config.Config, login, interactive bool) *Startup {
	return &Startup{exec: e, cfg: cfg, login: login, interactive: interactive, stdout: os.Stdout, stderr: os.Stderr, line: asItIs}
}

// Run runs the startup's steps in order: the login files and the profile
// commands (a login shell), migrated (the previous shell's config), then
// the interactive files and rc_commands (an interactive shell), as bash and
// zsh run the system's files before the user's. It returns at the first
// step that ends the shell, with how it did.
func (st *Startup) Run(ctx context.Context, migrated func(context.Context) StepOutcome) StepOutcome {
	shell := st.cfg.Shell
	if st.login {
		if out := st.files(ctx, shell.StartupFiles.Login); out.End != StepDone {
			return out
		}
		if out := st.commands(ctx, "profile", shell.ProfileCommands); out.End != StepDone {
			return out
		}
	}
	if out := migrated(ctx); out.End != StepDone {
		return out
	}
	if st.interactive {
		if out := st.files(ctx, shell.StartupFiles.Interactive); out.End != StepDone {
			return out
		}
		return st.commands(ctx, "rc_commands", shell.RCCommands)
	}
	return StepOutcome{}
}

// files runs the startup files at paths, as configured, in order.
func (st *Startup) files(ctx context.Context, paths []string) StepOutcome {
	for _, path := range paths {
		if out := st.sourceFile(ctx, path); out.End != StepDone {
			return out
		}
	}
	return StepOutcome{}
}

// commands runs the config's commands, of the key named key, in order.
func (st *Startup) commands(ctx context.Context, key string, commands []string) StepOutcome {
	for _, cmd := range commands {
		if cmd = strings.TrimSpace(cmd); cmd == "" {
			continue
		}
		res, err := st.line(ctx, func(ctx context.Context) (*executor.Result, error) {
			return st.exec.Execute(ctx, cmd, st.stdout, st.stderr)
		})
		if err != nil {
			fmt.Fprintf(st.stderr, "hash: %s: %v\n", key, err)
		}
		if out := st.outcome(ctx, res, err); out.End != StepDone {
			return out
		}
	}
	return StepOutcome{}
}

// sourceFile is the step of the startup file at path, as configured: a
// missing one is passed over quietly.
func (st *Startup) sourceFile(ctx context.Context, path string) StepOutcome {
	path, err := startupFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return StepOutcome{}
	}
	if err == nil {
		trace.Emit("compat", "source_file_start", trace.LevelVerbose, map[string]any{"path": path})
		text, report, ferr := compat.FilterWithDialect(path, compat.ShellForPath(path), st.exec.Dialect())
		if ferr == nil {
			return st.runFile(ctx, path, text, report)
		}
		err = ferr
	}
	fmt.Fprintf(st.stderr, "hash: %v\n", compat.FileError(path, err))
	return StepOutcome{}
}

// startupFile returns the startup file at path, as configured (~
// expanded), or why it cannot run.
func startupFile(path string) (string, error) {
	path = tilde.Expand(path)
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		err = errors.New("is a directory")
	}
	return path, err
}

// runFile runs text, the startup file at path filtered (report, of it and
// maybe other files, written for report.SourceShell), and takes how it
// ended; a file that ran already in
// this startup is passed over. A file that did not load, or not all of it,
// says so in a line: it, or one it sourced, which report takes.
func (st *Startup) runFile(ctx context.Context, path, text string, report *compat.Report) StepOutcome {
	if !st.firstRun(path) {
		return StepOutcome{}
	}
	for _, n := range report.NotLoaded {
		if n.File == path {
			fmt.Fprintf(st.stderr, "hash: %s\n", n)
		}
	}
	var res *executor.StartupResult
	_, err := st.line(ctx, func(ctx context.Context) (*executor.Result, error) {
		var err error
		if res, err = st.exec.ExecuteStartup(ctx, tilde.Abbrev(path), report.SourceShell, text, st.stdout, st.stderr); err != nil {
			return nil, err
		}
		return res.Result, nil
	})
	trace.Emit("compat", "source_file_done", trace.LevelVerbose, map[string]any{"path": path, "error": fmt.Sprintf("%v", err)})
	if err != nil {
		fmt.Fprintf(st.stderr, "hash: %v\n", compat.FileError(path, err))
		return st.outcome(ctx, nil, err)
	}
	for _, path := range res.Sourced {
		st.firstRun(path)
	}
	for _, n := range res.NotLoaded {
		notLoaded := compat.NotLoaded{File: n.Path, Load: n.Load, Line: int(n.Line), Reason: n.Msg}
		fmt.Fprintf(st.stderr, "hash: %s\n", notLoaded)
		report.NotLoad(notLoaded)
	}
	return st.outcome(ctx, res.Result, nil)
}

// firstRun records that the file at path runs, and reports whether it is
// the first time in the startup: the same file by any name (a link).
func (st *Startup) firstRun(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	for _, ran := range st.ran {
		if os.SameFile(ran, info) {
			return false
		}
	}
	st.ran = append(st.ran, info)
	return true
}

// outcome is how a step that ran a line (res, or err) ended: the one place
// that decides whether startup goes on.
func (st *Startup) outcome(ctx context.Context, res *executor.Result, err error) StepOutcome {
	switch {
	case ctx.Err() != nil:
		return StepOutcome{End: StepCanceled}
	case err != nil:
		return StepOutcome{}
	case res.End == executor.LineExited, res.End == executor.LineFailed && !st.interactive:
		return StepOutcome{End: StepExited, Status: res.ExitCode}
	}
	return StepOutcome{}
}

// UseSourceFilter makes e filter a zsh file that source or . reads in the
// bash dialect, as a startup file is (compat.FilterSourced), at startup and
// after: the shell's and a script's executor.
func UseSourceFilter(e *executor.Executor) {
	e.SetSourceFilter(filterSourced)
}

// filterSourced is the source filter: what of src, the file at path
// sourced in a file written for from, loads in dialect
// (compat.FilterSourced).
func filterSourced(path string, src []byte, dialect, from string) executor.SourceFile {
	text, n := compat.FilterSourced(path, src, dialect, from)
	if n == nil {
		return executor.SourceFile{Load: executor.LoadedAll, Text: text}
	}
	return executor.SourceFile{Load: n.Load, Text: text, Line: uint(n.Line), Msg: n.Reason}
}

// startup is the shell's startup, for its mode: each step runs as a line
// typed at the prompt does, and $? is how it ended (syncStatus): 130 when
// Ctrl+C ended it, where the interpreter's is 1.
func (s *Shell) startup() *Startup {
	st := NewStartup(s.executor, s.config, s.mode.Login, s.mode.Interactive)
	st.line = func(ctx context.Context, run lineRun) (*executor.Result, error) {
		res, err := s.onLine(newLineContext(ctx), run)
		if err == nil {
			s.syncStatus(res.ExitCode)
		}
		return res, err
	}
	return st
}

// runStartup runs the interactive shell's startup (Startup.Run), with the
// previous shell's config as its migration step, then init_commands. It
// returns the shell's exit when a step ended the shell.
func (s *Shell) runStartup(ctx context.Context) error {
	if s.config == nil {
		return nil
	}
	st := s.startup()
	out := st.Run(ctx, func(ctx context.Context) StepOutcome {
		return s.runMigration(ctx, st, func() (string, bool) { return s.keyboard().answerLine(ctx, s.out()) }, s.out())
	})
	switch out.End {
	case StepExited:
		return shellExit{status: out.Status}
	case StepCanceled:
		return ctx.Err()
	}
	return s.runInitCommands(ctx)
}

// runMigration is the startup step for the previous shell's config. It is the
// only place startup reads or writes the migration state: on first run it
// offers to import, and once imported it re-sources the files on every
// interactive launch, noting any that changed since the last launch.
//
// readLine reads the first-run question's answer: the shell's keyboard, so
// Ctrl+C and hash ending (ctx) end the question as they end any other, and
// typeahead is not its answer (keyboard.answerLine).
func (s *Shell) runMigration(ctx context.Context, st *Startup, readLine func() (string, bool), out io.Writer) StepOutcome {
	if !s.mode.Interactive {
		return StepOutcome{}
	}

	statePath := compat.DefaultStatePath()
	state, err := compat.ReadMigration(statePath)
	if err != nil {
		fmt.Fprintf(s.errOut(), "hash: migration state: %v (run 'hash migrate' to import again)\n", err)
		return StepOutcome{}
	}

	switch state.Status {
	case compat.StatusFirstRun:
		return s.firstRunMigration(ctx, st, statePath, readLine, out)
	case compat.StatusImported:
		return s.resourceMigration(ctx, st, statePath, state, out)
	}
	return StepOutcome{} // manual or declined: nothing to source
}

const firstRunMigrationHelp = `
Hash can source your existing config with compatibility filtering:

  Aliases, environment variables, functions, and PATH changes are imported.
  Zsh-specific setup such as bindkey, setopt, and compdef is skipped.
  Your original config files are not modified.
`

// firstRunMigration offers to import the previous shell's config. Yes imports
// now (sourcing the files once), no records the decline, and no answer (Esc,
// Ctrl+C, Ctrl+D, hash ending) saves nothing so the offer comes back on the
// next launch.
func (s *Shell) firstRunMigration(ctx context.Context, st *Startup, statePath string, readLine func() (string, bool), out io.Writer) StepOutcome {
	shellFiles := compat.DetectPreviousShellFiles()
	paths := shellFiles.Files()
	if len(paths) == 0 {
		return StepOutcome{}
	}

	fmt.Fprintln(out, compat.FormatWelcomePromptFiles(shellFiles))
	switch compat.AskMigration(readLine, out, "Load these settings now?", firstRunMigrationHelp) {
	case compat.DecisionImport:
		return s.importFirstRunMigration(ctx, st, statePath, shellFiles.Shell, paths, out)
	case compat.DecisionDecline:
		if err := compat.RecordDecline(statePath, shellFiles.Shell, paths); err != nil {
			fmt.Fprintf(s.errOut(), "hash: could not save migration state: %v\n", err)
		}
		fmt.Fprintln(out, "\nNo problem. Run 'hash migrate' anytime to import settings.")
	case compat.DecisionLater:
		fmt.Fprintln(out, "Skipping migration for now. Run 'hash migrate' anytime to import settings.")
	}
	return StepOutcome{}
}

// importFirstRunMigration sources the files at paths and records the
// import, even when a file ended the shell: the import was asked for.
func (s *Shell) importFirstRunMigration(ctx context.Context, st *Startup, statePath, shell string, paths []string, out io.Writer) StepOutcome {
	fmt.Fprint(out, "\nLoading settings... ")
	files, report, ran := st.sourceMigrated(ctx, paths, shell)
	if report == nil {
		fmt.Fprintf(s.errOut(), "\nhash: no files could be loaded\n")
		return ran
	}

	fmt.Fprintln(out, "done!")
	fmt.Fprintln(out)
	fmt.Fprintln(out, compat.FormatImportSummary(report, false))

	_, hashrcErr, err := compat.RecordImport(statePath, shell, files, report)
	switch {
	case err != nil:
		fmt.Fprintf(s.errOut(), "hash: could not save the migration, the offer comes back next launch: %v\n", err)
	case hashrcErr != nil:
		fmt.Fprintf(s.errOut(), "hash: %v\n", hashrcErr)
	}
	fmt.Fprintln(out)
	return ran
}

// resourceMigration sources an imported migration's files and records their
// new mtimes, printing a one-line notice (once per change) when any changed.
func (s *Shell) resourceMigration(ctx context.Context, st *Startup, statePath string, state *compat.State, out io.Writer) StepOutcome {
	trace.Emit("compat", "migration_source_start", trace.LevelVerbose, map[string]any{
		"files":          state.Paths,
		"shell":          state.SourceShell,
		"target_dialect": st.exec.Dialect(),
	})

	files, report, ran := st.sourceMigrated(ctx, state.Paths, state.SourceShell)

	changed, updated := state.Resourced(files, report)
	if updated {
		if err := state.Save(statePath); err != nil {
			fmt.Fprintf(s.errOut(), "hash: could not save migration state: %v\n", err)
		}
	}
	if len(changed) > 0 {
		fmt.Fprint(out, compat.FormatChangeNotice(changed, state.Import.Summary.Skipped))
	}

	trace.Emit("compat", "migration_source_done", trace.LevelVerbose, nil)
	return ran
}

// Migrated is the migration step of a non-interactive hash that reads
// startup files (hash -lc, -ic): an imported migration's files that the
// previous shell reads in its mode (State.PathsFor), sourced as the
// interactive shell sources them, without its notices or the record of
// their mtimes, which are the interactive shell's.
func (st *Startup) Migrated(ctx context.Context) StepOutcome {
	state, err := compat.LoadState(compat.DefaultStatePath())
	if err != nil || state.Status != compat.StatusImported {
		return StepOutcome{} // none, or the interactive shell says what is wrong with it
	}
	_, _, ran := st.sourceMigrated(ctx, state.PathsFor(st.login, st.interactive), state.SourceShell)
	return ran
}

// sourceMigrated filters the migrated files at paths, written for shell,
// for the executor's dialect and runs them in order, so that aliases,
// exports, and functions persist to the session; it stops at a file that
// ends the shell. files and report are all of them filtered, for the
// migration's record.
func (st *Startup) sourceMigrated(ctx context.Context, paths []string, shell string) (files []compat.FilteredFile, report *compat.Report, ran StepOutcome) {
	files, report, errs := compat.FilterFiles(paths, shell, st.exec.Dialect())
	for _, err := range errs {
		fmt.Fprintf(st.stderr, "hash: %v\n", err)
	}
	for _, f := range files {
		trace.Emit("compat", "migration_execute", trace.LevelVerbose, map[string]any{
			"file":            f.Path,
			"target_dialect":  st.exec.Dialect(),
			"filtered_length": len(f.Content),
		})
		if ran = st.runFile(ctx, f.Path, f.Content, report); ran.End != StepDone {
			break
		}
	}
	return files, report, ran
}
