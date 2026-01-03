package clipboard

// entry represents a command and its output.
type entry struct {
	command string
	output  string
}

// Buffer stores recent commands and their outputs.
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

	b.entries = append(b.entries, entry{
		command: cmd,
	})
}

// SetOutput sets the output for the most recent command.
func (b *Buffer) SetOutput(output string) {
	if len(b.entries) == 0 {
		return
	}

	// Truncate if too large
	if len(output) > b.maxOutputSize {
		output = output[:b.maxOutputSize]
	}

	b.entries[len(b.entries)-1].output = output
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
