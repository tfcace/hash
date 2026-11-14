package clipboard

import (
	"unicode/utf8"

	"github.com/tfcace/hash/internal/termtext"
)

// entry represents a command and its output.
type entry struct {
	command string
	output  string // OutputCaptured's text
	state   OutputState
}

// OutputState is what became of a command's output.
type OutputState int

const (
	// OutputAwaiting: the command was added and its output not set yet.
	// Only the newest entry can await it.
	OutputAwaiting OutputState = iota
	// OutputCaptured: hash kept what it printed.
	OutputCaptured
	// OutputNone: it printed nothing hash saw.
	OutputNone
	// OutputUncaptured: it wrote straight to the terminal (a pipeline's
	// tail at the prompt), which hash never sees.
	OutputUncaptured
)

// Buffer stores recent commands and their outputs. AddCommand opens an
// entry that awaits its output; SetOutput or SetUncaptured settles that
// entry, once, so output can never land on an older command.
type Buffer struct {
	entries       []entry
	maxEntries    int
	maxOutputSize int
}

// NewBuffer creates a new buffer with the given max entries.
func NewBuffer(maxEntries int) *Buffer {
	if maxEntries <= 0 {
		maxEntries = 100
	}
	return &Buffer{
		entries:       make([]entry, 0, maxEntries),
		maxEntries:    maxEntries,
		maxOutputSize: 1024 * 1024, // 1MB default
	}
}

// SetMaxOutputSize sets the maximum output size per entry.
func (b *Buffer) SetMaxOutputSize(size int) {
	b.maxOutputSize = size
}

// AddCommand adds a new command to the buffer.
func (b *Buffer) AddCommand(cmd string) {
	// Evict oldest if at capacity
	if len(b.entries) >= b.maxEntries {
		b.entries = b.entries[1:]
	}

	b.entries = append(b.entries, entry{command: cmd})
}

// awaiting is the newest entry when it awaits its output, else nil.
func (b *Buffer) awaiting() *entry {
	if len(b.entries) == 0 || b.entries[len(b.entries)-1].state != OutputAwaiting {
		return nil
	}
	return &b.entries[len(b.entries)-1]
}

// SetOutput sets the output of the command AddCommand added last, once; with
// no command awaiting output it does nothing. Output is captured from a
// terminal: the buffer keeps the text it shows (termtext.Plain), so what
// `copy out` and Ctrl+O put on the clipboard is text, not terminal bytes.
// An empty output is OutputNone.
func (b *Buffer) SetOutput(output string) {
	e := b.awaiting()
	if e == nil {
		return
	}
	e.output = b.cut(termtext.Plain(output))
	e.state = OutputCaptured
	if e.output == "" {
		e.state = OutputNone
	}
}

// SetUncaptured settles the command AddCommand added last as one that
// wrote straight to the terminal, past hash; with no command awaiting
// output it does nothing.
func (b *Buffer) SetUncaptured() {
	if e := b.awaiting(); e != nil {
		e.state = OutputUncaptured
	}
}

// cut cuts output to the size limit.
func (b *Buffer) cut(output string) string {
	if b.maxOutputSize < 0 {
		return output
	}
	if b.maxOutputSize == 0 {
		return ""
	}

	// Truncate if too large, at a character boundary
	if len(output) > b.maxOutputSize {
		cut := b.maxOutputSize
		for cut > 0 && !utf8.RuneStart(output[cut]) {
			cut--
		}
		output = output[:cut]
	}
	return output
}

// Len returns the number of entries in the buffer.
func (b *Buffer) Len() int {
	return len(b.entries)
}

// GetCommand returns the command at the given index (0 = most recent).
func (b *Buffer) GetCommand(index int) string {
	idx := len(b.entries) - 1 - index
	if idx < 0 || idx >= len(b.entries) {
		return ""
	}
	return b.entries[idx].command
}

// GetOutput returns the output at the given index (0 = most recent).
func (b *Buffer) GetOutput(index int) string {
	idx := len(b.entries) - 1 - index
	if idx < 0 || idx >= len(b.entries) {
		return ""
	}
	return b.entries[idx].output
}

// OutputState returns what became of the output at the given index (0 =
// most recent); OutputNone when there is no entry there.
func (b *Buffer) OutputState(index int) OutputState {
	idx := len(b.entries) - 1 - index
	if idx < 0 || idx >= len(b.entries) {
		return OutputNone
	}
	return b.entries[idx].state
}

// LastCommand returns the most recent command.
func (b *Buffer) LastCommand() string {
	return b.GetCommand(0)
}

// LastOutput returns the most recent output.
func (b *Buffer) LastOutput() string {
	return b.GetOutput(0)
}

// GetBoth returns both command and output at the given index.
func (b *Buffer) GetBoth(index int) (command, output string) {
	idx := len(b.entries) - 1 - index
	if idx < 0 || idx >= len(b.entries) {
		return "", ""
	}
	return b.entries[idx].command, b.entries[idx].output
}

// Clear clears the buffer.
func (b *Buffer) Clear() {
	b.entries = b.entries[:0]
}
