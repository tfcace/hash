package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/tfcace/hash/internal/shparse"
	"github.com/tfcace/hash/internal/trace"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// eval, source and alias bodies are parsed by hash (with the configured
// dialect, sanitized, background jobs marked) and then run by the
// interpreter's own eval and source builtins, in place in the calling
// shell: the body's status, exit and return act on the caller as in bash,
// a sourced file may end with a top-level return, and neither $0 nor the
// EXIT trap is touched. The builtins parse bash, so the program is printed
// back as bash first.

// bashText prints prog as bash source for the eval and source builtins. ok
// is false when bash cannot express it (zsh-only syntax in the zsh dialect).
func (e *Executor) bashText(prog *syntax.File) (text string, ok bool) {
	var b strings.Builder
	if err := syntax.NewPrinter().Print(&b, prog); err != nil {
		return "", false
	}
	if e.lang != syntax.LangBash {
		if _, err := shparse.Parse(syntax.NewParser(), b.String(), ""); err != nil {
			return "", false
		}
	}
	return b.String(), true
}

// runNested runs a program bash cannot express on the calling runner and
// returns the call that carries its exit status. An exit or return in it
// acts on the caller as in bash (the shell exits, the function returns):
// the runner keeps the state the program left.
//
// It runs as a File named after the caller's script, so $0 stays the
// caller's inside the program and after it: Runner.Run starts every
// program with the File's name ("gosh" for any other node). Running a File
// would fire the runner's EXIT trap, but the interpreter never holds one
// (see shellTraps).
func runNested(ctx context.Context, runner *interp.Runner, prog *syntax.File, source string) []string {
	trace.Emit("compat", "nested_run", trace.LevelVerbose, map[string]any{"source": source})
	nested := &syntax.File{Stmts: prog.Stmts}
	if name, ok := runnerScriptName(runner); ok {
		nested.Name = name
	}
	err := safeRunNode(ctx, runner, nested)
	if err != nil && strings.HasPrefix(err.Error(), "interpreter panic:") {
		// If the whole program panicked, try statement-by-statement
		err = runStatementsWithRecovery(ctx, runner, prog, source)
	}
	return statusCall(exitCodeFromError(err))
}

// pendingSources hands a parsed file to the source builtin: the source
// handler stores the printed program under a key of its own and calls the
// builtin with that key for the path, and the builtin's open of the key
// reads the text. The key is the file's path with a NUL and a serial number
// appended: no real open can name it, so the text never reaches another
// read of the file, and two jobs sourcing one file each get their own.
type pendingSources struct {
	mu    sync.Mutex
	next  uint64
	texts map[string]string // by key
}

// put stores text for the source of path and returns the key to open.
func (p *pendingSources) put(path, text string) (key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.texts == nil {
		p.texts = make(map[string]string)
	}
	p.next++
	key = fmt.Sprintf("%s\x00%d", path, p.next)
	p.texts[key] = text
	return key
}

func (p *pendingSources) take(key string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	text, ok := p.texts[key]
	delete(p.texts, key)
	return text, ok
}

// open is the runner's open handler: a pending source's text, or the file.
func (p *pendingSources) open(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	if flag == os.O_RDONLY && strings.ContainsRune(path, 0) {
		if text, ok := p.take(path); ok {
			return sourceText{strings.NewReader(text)}, nil
		}
	}
	return interp.DefaultOpenHandler()(ctx, path, flag, perm)
}

// sourceText is a pending source's text as the read-only file the source
// builtin opens.
type sourceText struct{ *strings.Reader }

func (sourceText) Write([]byte) (int, error) { return 0, errors.New("read-only") }
func (sourceText) Close() error              { return nil }
