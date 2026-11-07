package shell

import (
	"bytes"
	"context"
	"testing"

	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
)

func TestBuiltinCd_Disabled(t *testing.T) {
	cfg := &config.Config{
		Shell: config.ShellConfig{
			DisableBuiltins: []string{"cd"},
		},
	}

	// With cd disabled, isBuiltinEnabled should return false
	if isBuiltinEnabled(cfg, "cd") {
		t.Error("cd should be disabled")
	}

	// exit should still work
	if !isBuiltinEnabled(cfg, "exit") {
		t.Error("exit should still be enabled")
	}
}

func TestBuiltinEnabled_AllEnabled(t *testing.T) {
	cfg := &config.Config{
		Shell: config.ShellConfig{
			DisableBuiltins: []string{}, // Nothing disabled
		},
	}

	// All builtins should be enabled
	if !isBuiltinEnabled(cfg, "cd") {
		t.Error("cd should be enabled")
	}
	if !isBuiltinEnabled(cfg, "exit") {
		t.Error("exit should be enabled")
	}
	if !isBuiltinEnabled(cfg, "history") {
		t.Error("history should be enabled")
	}
}

func TestBuiltinEnabled_MultipleDisabled(t *testing.T) {
	cfg := &config.Config{
		Shell: config.ShellConfig{
			DisableBuiltins: []string{"cd", "history"},
		},
	}

	if isBuiltinEnabled(cfg, "cd") {
		t.Error("cd should be disabled")
	}
	if isBuiltinEnabled(cfg, "history") {
		t.Error("history should be disabled")
	}
	if !isBuiltinEnabled(cfg, "exit") {
		t.Error("exit should still be enabled")
	}
}

func TestBuiltinCopy_IsBuiltin(t *testing.T) {
	// copy should be a builtin
	if !isBuiltin("copy") {
		t.Error("copy should be a builtin")
	}
}

func TestBuiltinCopy_Cmd(t *testing.T) {
	// Test that we can copy the last command
	buf := clipboard.NewBuffer(10)
	buf.AddCommand("ls -la")
	buf.SetOutput("file1\nfile2")

	cmd := buf.GetCommand(0)
	if cmd != "ls -la" {
		t.Errorf("GetCommand(0) = %q, want %q", cmd, "ls -la")
	}
}

func TestBuiltinCopy_Out(t *testing.T) {
	// Test that we can copy the last output
	buf := clipboard.NewBuffer(10)
	buf.AddCommand("ls -la")
	buf.SetOutput("file1\nfile2")

	out := buf.GetOutput(0)
	if out != "file1\nfile2" {
		t.Errorf("GetOutput(0) = %q, want %q", out, "file1\nfile2")
	}
}

func TestBuiltinCopy_CmdN(t *testing.T) {
	// Test that we can copy the Nth command
	buf := clipboard.NewBuffer(10)
	buf.AddCommand("cmd1")
	buf.AddCommand("cmd2")
	buf.AddCommand("cmd3")

	// copy cmd 2 should get "cmd2" (2nd to last)
	cmd := buf.GetCommand(1) // 0-indexed, so 1 = 2nd to last
	if cmd != "cmd2" {
		t.Errorf("GetCommand(1) = %q, want %q", cmd, "cmd2")
	}
}

func TestBuiltinCopy_All(t *testing.T) {
	// Test that we can copy command + output
	buf := clipboard.NewBuffer(10)
	buf.AddCommand("ls -la")
	buf.SetOutput("file1\nfile2")

	cmd, out := buf.GetBoth(0)
	if cmd != "ls -la" || out != "file1\nfile2" {
		t.Errorf("GetBoth(0) = (%q, %q), want (%q, %q)", cmd, out, "ls -la", "file1\nfile2")
	}
}

// zsh's setup builtins typed at the prompt are the interpreter's no-ops,
// not builtins of the shell's own.
func TestNoopBuiltin_RunsInTheInterpreter(t *testing.T) {
	for _, name := range []string{"bindkey", "setopt"} {
		if isBuiltin(name) {
			t.Errorf("%s is a shell builtin", name)
		}
	}
	sh := &Shell{executor: executor.New()}
	if handled, _ := sh.executeBuiltin(context.Background(), "setopt autocd"); handled {
		t.Error("the shell ran setopt itself")
	}
	var out, errOut bytes.Buffer
	if _, err := sh.executor.Execute(context.Background(), "bindkey -e; setopt autocd && echo ok", &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "ok\n" || errOut.Len() != 0 {
		t.Errorf("stdout %q stderr %q, want ok", out.String(), errOut.String())
	}
}
