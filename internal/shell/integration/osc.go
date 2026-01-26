// Package integration provides terminal integration escape sequences.
// Supports OSC 133 (shell integration) and OSC 7 (directory reporting).
package integration

import (
	"fmt"
	"io"
	"os"
)

// Emitter handles terminal integration escape sequences.
type Emitter struct {
	w io.Writer
}

// New creates an Emitter writing to stdout.
func New() *Emitter {
	return &Emitter{w: os.Stdout}
}

// NewWithWriter creates an Emitter with a custom writer (for testing).
func NewWithWriter(w io.Writer) *Emitter {
	return &Emitter{w: w}
}

// Shell integration (OSC 133)

// PromptStart marks the beginning of prompt rendering (OSC 133;A).
func (e *Emitter) PromptStart() {
	e.emit("133;A")
}

// CommandStart marks the end of prompt, beginning of user input area (OSC 133;B).
func (e *Emitter) CommandStart() {
	e.emit("133;B")
}

// CommandExecuted marks the start of command output (OSC 133;C).
func (e *Emitter) CommandExecuted() {
	e.emit("133;C")
}

// CommandFinished marks command completion with exit status (OSC 133;D).
func (e *Emitter) CommandFinished(exitCode int) {
	e.emitf("133;D;%d", exitCode)
}

// Working directory (OSC 7)

// ReportDirectory reports the current working directory to the terminal (OSC 7).
// This enables terminals to open new tabs/panes in the same directory.
func (e *Emitter) ReportDirectory(path string) {
	hostname, _ := os.Hostname()
	e.emitf("7;file://%s%s", hostname, path)
}

// Internal helpers

func (e *Emitter) emit(seq string) {
	fmt.Fprintf(e.w, "\x1b]%s\x07", seq)
}

func (e *Emitter) emitf(format string, args ...any) {
	e.emit(fmt.Sprintf(format, args...))
}
