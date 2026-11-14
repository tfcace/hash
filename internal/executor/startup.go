package executor

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/tfcace/hash/internal/shparse"
	"github.com/tfcace/hash/internal/trace"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// StartupResult is how a startup file's text ran (ExecuteStartup).
type StartupResult struct {
	*Result
	// Sourced lists the files source and . read as it ran, in order: a
	// startup runs each of its files once.
	Sourced []string
	// NotLoaded lists the files source and . read that did not load, or
	// not all of them, in order: the shell says so.
	NotLoaded []SourceNotLoaded
}

// SourceNotLoaded is a file at Path that source read in a startup file
// that did not load, or not all of it (SourceFile): Load is LoadedBefore
// or LoadedNone, from Line (0: no line), for Msg.
type SourceNotLoaded struct {
	Path string
	Load SourceLoad
	Line uint
	Msg  string
}

// ExecuteStartup runs a startup file's text (an rc or profile file, a
// migrated config), named name ("": none), written for shell ("": hash),
// as Execute does, except that it shows the terminal its name in place of
// its text (cmdStatus), runs as a sourced file (a return at its top level
// ends it, see sourcedProgram), a file it sources goes through the source
// filter as one sourced in a file of shell's, and a syntax error in an
// eval or source in it is skipped quietly: a line written for another
// shell must not stop the shell from starting.
func (e *Executor) ExecuteStartup(ctx context.Context, name, shell, text string, stdout, stderr io.Writer) (*StartupResult, error) {
	sr := &startupRun{name: name, shell: shell}
	res, err := e.executeLine(context.WithValue(ctx, startupRunKey{}, sr), text, stdout, stderr, lineStartup)
	if err != nil {
		return nil, err
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return &StartupResult{Result: res, Sourced: slices.Clone(sr.sourced), NotLoaded: slices.Clone(sr.notLoaded)}, nil
}

// startupRun is what ExecuteStartup's line collects for StartupResult as
// it runs, and its background jobs after. Handlers find it in their
// context; outside a startup file's line there is none.
type startupRun struct {
	name      string // what the terminal is shown as the line's text
	shell     string // the shell the startup file is written for, "" hash
	mu        sync.Mutex
	sourced   []string
	notLoaded []SourceNotLoaded
}

type startupRunKey struct{}

// startupRunFrom returns the startup file's run a handler call belongs to,
// or nil.
func startupRunFrom(ctx context.Context) *startupRun {
	sr, _ := ctx.Value(startupRunKey{}).(*startupRun)
	return sr
}

// SourceLoad is how much of a file that source or . reads loads.
type SourceLoad int

const (
	// LoadedAll: all of it.
	LoadedAll SourceLoad = iota
	// LoadedBefore: the complete statements before one that does not
	// parse, as bash's source runs them.
	LoadedBefore
	// LoadedNone: none of it: its first statement does not parse, or it
	// could not be filtered.
	LoadedNone
)

// SourceFile is what of a file that source or . reads loads (Load), as the
// source filter made it: Text, the text that runs of it. One that does not
// load all says why (Msg), at Line (0: no line).
type SourceFile struct {
	Load SourceLoad
	Text []byte
	Line uint
	Msg  string
}

// loadSource is what of content, the file at path, sourced in the startup
// file of sr (nil: outside one), loads: the source filter's word on it,
// then the parser's on the text it kept, with prog, the program of what
// loads (nil when none of it does). A file that does not parse runs the
// statements before the error, as bash's source does, but none of it in
// a startup file: a statement before the error may be a guard that is
// wrong of a file written for another shell (oh-my-zsh.sh's printed an
// error and the process tree at each start).
func (e *Executor) loadSource(path string, content []byte, sr *startupRun) (sf SourceFile, prog *syntax.File) {
	if sf = e.filter.apply(path, content, e.Dialect(), sr.writtenFor()); sf.Load == LoadedNone {
		return sf, nil
	}
	src, sanitized := sanitizeUnsupportedExpansions(string(sf.Text))
	if sanitized {
		trace.Emit("compat", "source_sanitize", trace.LevelVerbose, map[string]any{"path": path})
	}
	prog, err := shparse.Parse(e.newParser(), src, path)
	if err == nil {
		return sf, prog
	}
	trace.Emit("compat", "source_parse_error", trace.LevelVerbose, map[string]any{"path": path, "error": err.Error()})
	line, msg := SyntaxError(err)
	var stmts []*syntax.Stmt
	if sr == nil {
		_ = shparse.Stmts(e.newParser(), strings.NewReader(src), func(s *syntax.Stmt) bool {
			stmts = append(stmts, s)
			return true
		})
	}
	if len(stmts) == 0 {
		return SourceFile{Load: LoadedNone, Line: line, Msg: msg}, nil
	}
	return SourceFile{Load: LoadedBefore, Text: []byte(src), Line: line, Msg: msg}, &syntax.File{Name: path, Stmts: stmts}
}

// notLoaded says that the file at path (name as written) did not load, or
// not all of it (sf): in a startup file's line it is the shell's to say
// (StartupResult.NotLoaded), else it is printed as bash prints a syntax
// error.
func (e *Executor) notLoaded(ctx context.Context, name, path string, sf SourceFile) {
	if sr := startupRunFrom(ctx); sr != nil {
		sr.notLoad(SourceNotLoaded{Path: path, Load: sf.Load, Line: sf.Line, Msg: sf.Msg})
		return
	}
	stderr := interp.HandlerCtx(ctx).Stderr
	if sf.Line == 0 {
		fmt.Fprintf(stderr, "hash: %s: %s\n", name, sf.Msg)
		return
	}
	fmt.Fprintf(stderr, "hash: %s: line %d: %s\n", name, sf.Line, sf.Msg)
}

// notLoad records a file source read that did not load.
func (sr *startupRun) notLoad(n SourceNotLoaded) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.notLoaded = append(sr.notLoaded, n)
}

// shown is what the terminal is shown of command, a line's text, as it
// runs: a startup file's name.
func (sr *startupRun) shown(command string) string {
	if sr == nil || sr.name == "" {
		return command
	}
	return sr.name
}

// writtenFor is the shell the startup file is written for: "" for hash's,
// or outside a startup file.
func (sr *startupRun) writtenFor() string {
	if sr == nil {
		return ""
	}
	return sr.shell
}

// read records that source read the file at path.
func (sr *startupRun) read(path string) {
	if sr == nil {
		return
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.sourced = append(sr.sourced, path)
}

// lineProgram parses command, the text of a line of kind, for $0 name. A
// startup file's text runs as a sourced file does (sourcedProgram).
func (e *Executor) lineProgram(command, name string, kind lineKind) (*syntax.File, error) {
	prog, err := shparse.Parse(e.newParser(), command, name)
	if err != nil || kind != lineStartup {
		return prog, err
	}
	return e.sourcedProgram(prog), nil
}

// sourcedProgram returns the program that runs prog, a startup file's, as
// the source builtin runs a file: a return at its top level ends it, as in
// bash and zsh, where a line's would be an error. The interpreter's source
// builtin reads the printed program (pendingSources), called as `builtin
// source` so that a function named source does not take the call. A
// program bash cannot express (zsh-only syntax in the zsh dialect) runs as
// a line.
func (e *Executor) sourcedProgram(prog *syntax.File) *syntax.File {
	e.prepare(prog)
	text, ok := e.bashText(prog)
	if !ok {
		return prog
	}
	call := &syntax.CallExpr{Args: []*syntax.Word{
		litWord("builtin"), litWord("source"), litWord(e.sources.put("startup", text)),
	}}
	return &syntax.File{Name: prog.Name, Stmts: []*syntax.Stmt{{Cmd: call}}}
}

// sourceFilter is what the text of a file that source or . reads in a
// file written for from ("": hash, or none) goes through before hash
// parses it in dialect (SetSourceFilter): the shell filters a zsh file for
// the bash dialect there, as it does its startup files, and a startup
// file of a migrated file's shell that the migrated file sources.
type sourceFilter func(path string, src []byte, dialect, from string) SourceFile

// SetSourceFilter installs fn as the filter of every file that source or .
// reads, which the shell does as it makes its executor. What it says does
// not load of a file does not run, and is said (notLoaded).
func (e *Executor) SetSourceFilter(fn func(path string, src []byte, dialect, from string) SourceFile) {
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()
	e.filter = fn
}

// apply is what of src, the text of the file at path sourced in a file
// written for from, loads, filtered for dialect: all of it, as it is,
// without a filter.
func (f sourceFilter) apply(path string, src []byte, dialect, from string) SourceFile {
	if f == nil {
		return SourceFile{Load: LoadedAll, Text: src}
	}
	return f(path, src, dialect, from)
}

// zshSetupBuiltins are zsh's builtins for setting up the shell (options,
// key bindings, completion, modules, hooks), and the functions zsh
// autoloads for it, which hash has its own ways for: a call of one does
// nothing and succeeds, wherever it is, unless a function of the name is
// defined (the exec handler is not reached then).
var zshSetupBuiltins = map[string]bool{
	"add-zsh-hook": true, "autoload": true, "bashcompinit": true, "bindkey": true, "compdef": true,
	"compinit": true, "promptinit": true, "setopt": true, "unsetopt": true, "zle": true,
	"zmodload": true, "zstyle": true,
}

// zshSetupCall runs a call of one of zsh's setup builtins, or is-at-least,
// and reports whether args was one.
func zshSetupCall(hc interp.HandlerContext, args []string) (ok bool, err error) {
	switch {
	case zshSetupBuiltins[args[0]]:
		return true, nil
	case args[0] == "is-at-least":
		if len(args) < 2 {
			return true, nil
		}
		version := hc.Env.Get("ZSH_VERSION").String()
		if len(args) > 2 {
			version = args[2]
		}
		if version == "" || isAtLeast(args[1], version) {
			return true, nil
		}
		return true, interp.ExitStatus(1)
	}
	return false, nil
}

// isAtLeast reports whether version is at least least, as zsh's
// is-at-least compares them: part by part, split at . and -, as numbers;
// a part of least that is not a number is passed over, and a part of
// version counts by the digits it ends in, with a 0 after its last part.
// Running out of either is success.
func isAtLeast(least, version string) bool {
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' })
	}
	parts := append(split(version), "0") // zsh's has a 0 after the version's parts
	for _, m := range split(least) {
		want, err := strconv.Atoi(m)
		if err != nil {
			continue
		}
		have := -1
		for have < 0 && len(parts) > 0 {
			if i := strings.LastIndexFunc(parts[0], func(r rune) bool { return r < '0' || r > '9' }); i < len(parts[0])-1 {
				have, _ = strconv.Atoi(parts[0][i+1:])
			}
			parts = parts[1:]
		}
		switch {
		case have < 0 || have > want:
			return true
		case have < want:
			return false
		}
	}
	return true
}

// bashInteractiveOptions are bash's shopt options for the interactive
// shell (history, the line editor, completion, cd, job and mail checks),
// which hash has its own ways for: shopt -s or -u of one does nothing,
// quietly. The interpreter refuses them as unsupported.
var bashInteractiveOptions = map[string]bool{
	"autocd": true, "cdspell": true, "checkhash": true, "checkjobs": true, "checkwinsize": true,
	"cmdhist": true, "complete_fullquote": true, "direxpand": true, "dirspell": true,
	"force_fignore": true, "histappend": true, "histreedit": true, "histverify": true,
	"hostcomplete": true, "huponexit": true, "interactive_comments": true, "lithist": true,
	"mailwarn": true, "no_empty_cmd_completion": true, "progcomp": true, "progcomp_alias": true,
	"promptvars": true,
}

// shoptCall is the call to run for args, a shopt call: shopt -s or -u
// without bash's interactive options (bashInteractiveOptions), ":" when
// they were all it named; shopt -q, which the interpreter refuses, as a
// query (shoptQuery). Any other shopt call goes as it is.
func shoptCall(args []string) []string {
	flags, names := shoptArgs(args[1:])
	quiet := slices.ContainsFunc(flags, func(f string) bool { return strings.Contains(f, "q") })
	sets := slices.ContainsFunc(flags, func(f string) bool { return strings.ContainsAny(f, "su") })
	switch {
	case quiet && !sets:
		return shoptQuery(withoutQuiet(flags), names)
	case !sets:
		return args
	}
	// -s and -u print nothing: -q changes nothing.
	call := append([]string{"shopt"}, withoutQuiet(flags)...)
	if slices.Contains(flags, "-o") {
		return append(call, names...)
	}
	kept := slices.DeleteFunc(slices.Clone(names), func(name string) bool { return bashInteractiveOptions[name] })
	if len(kept) == 0 {
		return []string{":"}
	}
	return append(call, kept...)
}

// shoptArgs splits args, shopt's, into its flags (-s, -oq) and the
// option names after them.
func shoptArgs(args []string) (flags, names []string) {
	for i, arg := range args {
		switch {
		case arg == "--":
			return flags, args[i+1:]
		case len(arg) < 2 || arg[0] != '-':
			return flags, args[i:]
		}
		flags = append(flags, arg)
	}
	return flags, nil
}

// withoutQuiet returns flags without -q in them.
func withoutQuiet(flags []string) []string {
	var out []string
	for _, f := range flags {
		if f = strings.ReplaceAll(f, "q", ""); f != "-" {
			out = append(out, f)
		}
	}
	return out
}

// shoptQuery is the call for shopt -q flags names: status 0 when every
// option names is on, else 1, printing nothing, as bash's. The
// interpreter keeps the options to itself: the call reads what shopt
// prints for them, where an option off, or one it does not know, says off.
func shoptQuery(flags, names []string) []string {
	if len(names) == 0 {
		return []string{":"}
	}
	words := append([]string{"builtin", "shopt"}, flags...)
	for _, name := range names {
		words = append(words, shellQuote(name))
	}
	return []string{"eval", `[[ "$(` + strings.Join(words, " ") + ` 2>/dev/null || echo off)" != *off* ]]`}
}
