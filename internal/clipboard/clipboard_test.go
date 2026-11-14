package clipboard

import (
	"testing"
)

func TestBuffer_AddCommand(t *testing.T) {
	buf := NewBuffer(10)

	buf.AddCommand("ls -la")
	buf.AddCommand("pwd")

	if buf.Len() != 2 {
		t.Errorf("Len() = %d, want 2", buf.Len())
	}
}

func TestBuffer_GetCommand(t *testing.T) {
	buf := NewBuffer(10)

	buf.AddCommand("cmd1")
	buf.AddCommand("cmd2")
	buf.AddCommand("cmd3")

	// Get most recent (index 0)
	cmd := buf.GetCommand(0)
	if cmd != "cmd3" {
		t.Errorf("GetCommand(0) = %q, want %q", cmd, "cmd3")
	}

	// Get second most recent
	cmd = buf.GetCommand(1)
	if cmd != "cmd2" {
		t.Errorf("GetCommand(1) = %q, want %q", cmd, "cmd2")
	}

	// Get oldest
	cmd = buf.GetCommand(2)
	if cmd != "cmd1" {
		t.Errorf("GetCommand(2) = %q, want %q", cmd, "cmd1")
	}

	// Out of bounds
	cmd = buf.GetCommand(10)
	if cmd != "" {
		t.Errorf("GetCommand(10) = %q, want empty", cmd)
	}
}

func TestBuffer_AddOutput(t *testing.T) {
	buf := NewBuffer(10)

	buf.AddCommand("ls")
	buf.SetOutput("file1\nfile2")

	output := buf.GetOutput(0)
	if output != "file1\nfile2" {
		t.Errorf("GetOutput(0) = %q, want %q", output, "file1\nfile2")
	}
}

func TestBuffer_MaxSize(t *testing.T) {
	buf := NewBuffer(3)

	buf.AddCommand("cmd1")
	buf.AddCommand("cmd2")
	buf.AddCommand("cmd3")
	buf.AddCommand("cmd4") // Should evict cmd1

	if buf.Len() != 3 {
		t.Errorf("Len() = %d, want 3 (max size)", buf.Len())
	}

	// cmd1 should be gone
	cmd := buf.GetCommand(2)
	if cmd != "cmd2" {
		t.Errorf("Oldest command = %q, want %q (cmd1 should be evicted)", cmd, "cmd2")
	}
}

func TestBuffer_LastCommand(t *testing.T) {
	buf := NewBuffer(10)

	if buf.LastCommand() != "" {
		t.Error("LastCommand on empty buffer should be empty")
	}

	buf.AddCommand("cmd1")
	buf.AddCommand("cmd2")

	if buf.LastCommand() != "cmd2" {
		t.Errorf("LastCommand = %q, want %q", buf.LastCommand(), "cmd2")
	}
}

func TestBuffer_LastOutput(t *testing.T) {
	buf := NewBuffer(10)

	if buf.LastOutput() != "" {
		t.Error("LastOutput on empty buffer should be empty")
	}

	buf.AddCommand("ls")
	buf.SetOutput("output1")
	buf.AddCommand("pwd")
	buf.SetOutput("output2")

	if buf.LastOutput() != "output2" {
		t.Errorf("LastOutput = %q, want %q", buf.LastOutput(), "output2")
	}
}

func TestBuffer_MaxOutputSize(t *testing.T) {
	buf := NewBuffer(10)
	buf.SetMaxOutputSize(100)

	buf.AddCommand("cmd")

	// Create a large output
	largeOutput := make([]byte, 200)
	for i := range largeOutput {
		largeOutput[i] = 'x'
	}

	buf.SetOutput(string(largeOutput))

	output := buf.GetOutput(0)
	if len(output) > 100 {
		t.Errorf("Output length = %d, should be truncated to ~100", len(output))
	}
}

// Output pairs with the command that awaits it: a second SetOutput, or one
// after Clear, does not overwrite an older command's output.
func TestBuffer_OutputPairsWithItsCommand(t *testing.T) {
	b := NewBuffer(10)
	b.AddCommand("ls")
	b.SetOutput("files")
	b.SetOutput("stray")
	if got := b.LastOutput(); got != "files" {
		t.Fatalf("LastOutput = %q, want files", got)
	}
	b.Clear()
	b.SetOutput("stray")
	if b.Len() != 0 {
		t.Fatalf("Len = %d after a stray SetOutput, want 0", b.Len())
	}
}

// Output captured from a terminal is terminal bytes: a PTY ends its lines
// with CRLF, and programs color it with SGR and draw progress with CR. The
// clipboard holds the text they show.
func TestBuffer_SetOutputKeepsText(t *testing.T) {
	buf := NewBuffer(10)
	buf.AddCommand("ls --color")
	buf.SetOutput("\x1b[1;34mdir\x1b[0m\r\nfile\r\n\x1b]8;;file:///x\x1b\\link\x1b]8;;\x1b\\\r\n10%\r100%\r\n")
	if got, want := buf.LastOutput(), "dir\nfile\nlink\n100%\n"; got != want {
		t.Errorf("LastOutput() = %q, want %q", got, want)
	}
}

// An output cut to the size limit ends on a whole character: the cut split
// a multibyte one, which put invalid UTF-8 on the clipboard.
func TestBuffer_TruncationKeepsWholeCharacters(t *testing.T) {
	b := NewBuffer(10)
	b.SetMaxOutputSize(4)
	b.AddCommand("echo")
	b.SetOutput("abc€def") // € is three bytes, from byte 3
	if got := b.LastOutput(); got != "abc" {
		t.Errorf("output = %q, want %q", got, "abc")
	}
}

// An entry awaits its output until it is settled, once: captured, none
// (an empty output), or uncaptured (it went straight to the terminal). A
// settled entry, or a buffer with no entry, takes nothing more.
func TestBuffer_OutputStates(t *testing.T) {
	b := NewBuffer(10)
	b.SetUncaptured() // nothing awaits: nothing happens
	b.SetOutput("lost")
	if b.Len() != 0 {
		t.Fatalf("a buffer with no command took output: %d entries", b.Len())
	}
	b.AddCommand("cmd")
	if b.OutputState(0) != OutputAwaiting {
		t.Errorf("a new entry is %v, want OutputAwaiting", b.OutputState(0))
	}
	for _, tc := range []struct {
		settle func()
		want   OutputState
		out    string
	}{
		{func() { b.SetOutput("hi\n") }, OutputCaptured, "hi\n"},
		{func() { b.SetOutput("") }, OutputNone, ""},
		{func() { b.SetUncaptured() }, OutputUncaptured, ""},
	} {
		b.AddCommand("cmd")
		tc.settle()
		b.SetOutput("late") // settled once: ignored
		b.SetUncaptured()
		if b.OutputState(0) != tc.want || b.LastOutput() != tc.out {
			t.Errorf("entry = %v %q, want %v %q", b.OutputState(0), b.LastOutput(), tc.want, tc.out)
		}
	}
	if b.OutputState(99) != OutputNone {
		t.Errorf("no entry reads as %v, want OutputNone", b.OutputState(99))
	}
}
