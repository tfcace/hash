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
