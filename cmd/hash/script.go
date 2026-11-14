package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/shell"
	"github.com/tfcace/hash/internal/shlex"
	"github.com/tfcace/hash/internal/shparse"
)

// script runs a non-interactive hash's commands (-c, a script file, or a
// script read from stdin) with the executor alone: no editor, prompt, agent
// or history. It reads startup files only when asked, as bash: the login
// files for a login shell (-l), the rc files for -i.
//
// One goroutine owns a script from start to end: it reads the script, runs
// it and ends it (end). A fatal signal (or a SIGINT that ends the script,
// see watchInterrupts) only cancels ctx, which stops what
// the script is doing (a command, a read of stdin, the wait for its next
// line) and so brings it to end.
type script struct {
	exec   *executor.Executor
	ctx    context.Context // ends on a fatal signal, with the signal as its cause
	status int             // the last command's exit status
	line   uint            // the line the command being run starts on
}

// stopReason is why a script stopped running its commands: the value its
// steps return, running while it goes on. Its end tells only a signal
// apart (see close); s.status holds the rest.
type stopReason int

const (
	running    stopReason = iota // it goes on
	stopped                      // its commands ended: they ran out, an exit, an error
	stopSignal                   // a fatal signal ended its context
)

// newScript is a script with $0, $1, ... from the mode's arguments. Its
// output goes straight through: there is no agent or copy to keep a copy
// for.
func newScript(mode scriptMode) *script {
	e := executor.New()
	e.SetPositionalArgs(mode.args)
	e.SetCaptureLimit(0)
	shell.UseSourceFilter(e)
	fatal, keeps := fatalSignals, []syscall.Signal{syscall.SIGQUIT}
	if mode.rc { // hash -i: an interactive shell, as bash -i
		e.UseInteractiveShell()
		fatal, keeps = interactiveFatalSignals, shellKeeps
	}
	ctx, end := signalContext(fatal, e.IgnoresSignal)
	watchInterrupts(e, end)
	watchSignals(e, keeps...)
	return &script{exec: e, ctx: ctx}
}

// runScript runs the mode's program, a -c command string, a script file
// or stdin, and exits hash. A missing script file exits with 127, an
// unreadable one with 126, as bash.
func runScript(mode scriptMode) {
	s := newScript(mode)
	stop := s.start(mode)
	if stop == running {
		stop = s.runProgram(mode)
	}
	s.end(stop)
}

// runProgram runs the mode's program once the shell started.
func (s *script) runProgram(mode scriptMode) stopReason {
	switch mode.source {
	case sourceCommand:
		s.line = 1
		prog, err := parse(mode.program)
		if err != nil {
			return s.syntaxError(err)
		}
		if stop := s.run(mode.program, prog); stop != running {
			return stop
		}
		return stopped
	case sourceFile:
		return s.runFile(mode.program)
	default:
		return s.runLines(newStdinLines(s.ctx, os.Stdin))
	}
}

func (s *script) runFile(path string) stopReason {
	f, err := openScript(path)
	if err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		fmt.Fprintf(os.Stderr, "hash: %s: %v\n", path, err)
		s.status = 126
		if errors.Is(err, fs.ErrNotExist) {
			s.status = 127
		}
		return stopped
	}
	defer f.Close()
	// A script that is a pipe (hash <(cmds)) can wait for its next line;
	// a fatal signal ends that wait.
	stop := context.AfterFunc(s.ctx, func() { _ = f.SetReadDeadline(time.Now()) })
	defer stop()
	return s.runLines(bufio.NewReader(f))
}

// start applies the command line's set options and reads the startup files
// the mode asks for. The program runs when it returns running: not after a
// bad option (status 2) or an exit in a startup file.
func (s *script) start(mode scriptMode) stopReason {
	if len(mode.options) > 0 {
		words := make([]string, len(mode.options))
		for i, opt := range mode.options {
			words[i] = shellQuote(opt)
		}
		res, err := s.exec.Execute(s.ctx, "set "+strings.Join(words, " "), os.Stdout, os.Stderr)
		if err != nil || res.ExitCode != 0 {
			s.status = 2
			return stopped
		}
	}
	if !mode.login && !mode.rc {
		return running
	}
	cfg := config.Load(config.Dir())
	if cfg.LoadIssue != nil {
		fmt.Fprint(os.Stderr, cfg.LoadIssue.Warning())
	}
	// Startup files are parsed as the interactive shell parses them; the
	// program itself is bash.
	if err := s.exec.SetDialect(cfg.Shell.Dialect); err != nil {
		fmt.Fprintf(os.Stderr, "hash: warning: %v; using bash dialect\n", err)
	}
	defer s.exec.SetDialect("bash") //nolint:errcheck // bash is always supported
	st := shell.NewStartup(s.exec, cfg, mode.login, mode.rc)
	out := st.Run(s.ctx, st.Migrated)
	switch out.End {
	case shell.StepExited:
		s.status = out.Status
		return stopped
	case shell.StepCanceled:
		return stopSignal
	}
	return running
}

// ran takes the outcome of a command the script ran: its status, and
// whether the script goes on. An exit ends it, and so does a fatal error
// (an expansion error), as it ends a non-interactive bash.
func (s *script) ran(res *executor.Result) stopReason {
	s.status = res.ExitCode
	switch {
	case s.ctx.Err() != nil:
		return stopSignal
	case res.End == executor.LineExited, res.End == executor.LineFailed:
		return stopped
	}
	return running
}

// shellQuote quotes s as one shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var errIsDirectory = errors.New("is a directory")

func openScript(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err == nil && info.IsDir() {
		f.Close()
		return nil, errIsDirectory
	}
	return f, nil
}

// lineReader is where a script's lines come from.
type lineReader interface {
	ReadString(delim byte) (string, error)
}

// runLines runs the script lines make up, a command at a time: a line, or
// the lines one command spans. It stops at exit, at the end of the input and
// at an error that ends the script.
func (s *script) runLines(lines lineReader) stopReason {
	var pending strings.Builder
	var lex shlex.Lexer // the quoting state at the end of pending
	var read uint       // lines read so far
	for {
		if pending.Len() == 0 {
			s.line = read + 1
		}
		line, err := lines.ReadString('\n')
		pending.WriteString(line)
		lex.Scan(line)
		read++
		if s.ctx.Err() != nil {
			return stopSignal
		}
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintf(os.Stderr, "hash: %v\n", err)
			s.status = 1
			return stopped
		}
		eof := err != nil
		src := pending.String()
		// The parser takes a backslash-newline at the end of its input as
		// the end, so an escaped newline is the lexer's to see.
		if !eof && lex.Continues() {
			continue
		}
		prog, perr := parse(src)
		if !eof && syntax.IsIncomplete(perr) {
			continue
		}
		pending.Reset()
		lex = shlex.Lexer{}
		if perr != nil {
			return s.syntaxError(perr)
		}
		// Blank lines and comments leave $? alone.
		if len(prog.Stmts) > 0 {
			if stop := s.run(src, prog); stop != running {
				return stop
			}
		}
		if eof {
			return stopped
		}
	}
}

// syntaxError reports a syntax error in the command starting at s.line,
// which ends the script with status 2, as bash.
func (s *script) syntaxError(err error) stopReason {
	line, msg := executor.SyntaxError(err)
	fmt.Fprintf(os.Stderr, "hash: line %d: %s\n", s.line+line-1, msg)
	s.status = 2
	return stopped
}

func parse(src string) (*syntax.File, error) {
	return shparse.Parse(syntax.NewParser(), src, "")
}

// run runs one complete command (or a -c string), parsed as prog; the
// script goes on while it returns running. An agent request (a command
// named ??) is refused without running any of it.
func (s *script) run(src string, prog *syntax.File) stopReason {
	if call := agentCall(prog); call != nil {
		fmt.Fprintf(os.Stderr, "hash: line %d: ??: the agent needs an interactive shell\n", s.line+call.Pos().Line()-1)
		src = "(exit 127)" // the status of a command that is not there, for $? too
	}
	result, err := s.exec.Execute(s.ctx, src, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash: %v\n", err)
		s.status = 1
		return stopped
	}
	return s.ran(result)
}

// agentCall is the first command named ?? in prog (unquoted, so `echo "??"`
// is not one), or nil. Only the interactive shell hands those to the agent;
// anywhere else ?? would be a glob.
func agentCall(prog *syntax.File) *syntax.CallExpr {
	var found *syntax.CallExpr
	syntax.Walk(prog, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && len(call.Args) > 0 && call.Args[0].Lit() == "??" {
			found = call
		}
		return found == nil
	})
	return found
}

// end is the one way a script ends, once it stopped for stop: it runs the
// traps and ends hash (endShell).
func (s *script) end(stop stopReason) {
	endShell(s.ctx, s.status, shellCloser{
		signalTrap: s.signalTrap,
		close: func(_ syscall.Signal, status int) (int, bool) {
			return s.close(status, stop != stopSignal)
		},
	})
}

// signalTrap runs the script's trap for sig with $? = status.
func (s *script) signalTrap(sig syscall.Signal, status int) (int, bool) {
	s.exec.SetLastStatus(status)
	code, exited, _ := s.exec.RunSignalTrap(context.Background(), sig, os.Stdout, os.Stderr)
	return code, exited
}

// jobStartGrace is how long a script that exits gives its background jobs
// to start their commands (see Executor.LetJobsStart): a script whose jobs
// end sooner exits sooner, and a job slower to reach its command (a long
// function before it) is cut off.
const jobStartGrace = 100 * time.Millisecond

// close runs the EXIT trap with $? = status. It does not wait for the
// background jobs, as bash does not, past the moment they need to start
// their commands (letJobsStart, not when a signal ends hash): a job's
// command keeps running after hash exits, and a job hash runs itself (a
// function, a builtin, a pipeline it copies) is cut off.
func (s *script) close(status int, letJobsStart bool) (int, bool) {
	s.exec.SetLastStatus(status)
	code, exited := s.exec.RunExitTrap(context.Background(), os.Stdout, os.Stderr)
	if letJobsStart {
		s.exec.LetJobsStart(jobStartGrace)
	}
	return code, exited
}

// newStdinLines reads a script from stdin so that the script's commands
// find the rest of it unread, as in bash: a file (hash < script.sh) is
// read in blocks and gives back what it read past the line (fileLines); a
// pipe, which cannot give bytes back, a byte at a time (pipeLines). A
// fatal signal (ctx) ends the wait of a pipe, even partway through a line.
func newStdinLines(ctx context.Context, f *os.File) lineReader {
	if info, err := f.Stat(); err == nil && info.Mode().IsRegular() {
		return &fileLines{f: f}
	}
	return &pipeLines{f: f, wait: func() error { return executor.WaitReadable(ctx, f) }}
}

// seekFile is what fileLines needs of stdin.
type seekFile interface {
	io.Reader
	io.Seeker
}

// fileLines reads the lines of a file in blocks, seeking back past each
// line it returns.
type fileLines struct {
	f   seekFile
	buf []byte
}

func (r *fileLines) ReadString(delim byte) (string, error) {
	if r.buf == nil {
		r.buf = make([]byte, 4096)
	}
	var line []byte
	for {
		n, err := r.f.Read(r.buf)
		if i := bytes.IndexByte(r.buf[:n], delim); i >= 0 {
			line = append(line, r.buf[:i+1]...)
			if extra := n - (i + 1); extra > 0 {
				if _, serr := r.f.Seek(int64(-extra), io.SeekCurrent); serr != nil {
					return string(line), serr
				}
			}
			return string(line), nil
		}
		line = append(line, r.buf[:n]...)
		if err != nil {
			return string(line), err
		}
	}
}

// pipeLines reads the lines of a pipe a byte at a time.
type pipeLines struct {
	f io.Reader
	// wait holds each read until the pipe has data, so a fatal signal can
	// end the wait even when the writer stops halfway through a line.
	wait func() error
}

func (r *pipeLines) ReadString(delim byte) (string, error) {
	var line []byte
	var b [1]byte
	for {
		if err := r.wait(); err != nil {
			return string(line), err
		}
		n, err := r.f.Read(b[:])
		if n == 1 {
			line = append(line, b[0])
			if b[0] == delim {
				return string(line), nil
			}
		}
		if err != nil {
			return string(line), err
		}
	}
}
