package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/config"
)

func TestBuiltinCd(t *testing.T) {
	original, _ := os.Getwd()
	defer os.Chdir(original)

	tmpDir := t.TempDir()
	// Resolve symlinks (macOS /var -> /private/var)
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	err := builtinCd([]string{tmpDir})
	if err != nil {
		t.Fatalf("builtinCd() error = %v", err)
	}

	cwd, _ := os.Getwd()
	if cwd != tmpDir {
		t.Errorf("cwd = %q, want %q", cwd, tmpDir)
	}
}

func TestBuiltinCd_Home(t *testing.T) {
	original, _ := os.Getwd()
	defer os.Chdir(original)

	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}

	err := builtinCd([]string{})
	if err != nil {
		t.Fatalf("builtinCd() error = %v", err)
	}

	cwd, _ := os.Getwd()
	if cwd != home {
		t.Errorf("cwd = %q, want %q", cwd, home)
	}
}

func TestBuiltinCd_Tilde(t *testing.T) {
	original, _ := os.Getwd()
	defer os.Chdir(original)

	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}

	err := builtinCd([]string{"~"})
	if err != nil {
		t.Fatalf("builtinCd() error = %v", err)
	}

	cwd, _ := os.Getwd()
	if cwd != home {
		t.Errorf("cwd = %q, want %q", cwd, home)
	}
}

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

func TestNoopBuiltin_Bindkey(t *testing.T) {
	// bindkey should be recognized as a builtin
	if !isBuiltin("bindkey") {
		t.Error("bindkey should be a builtin")
	}
}

func TestNoopBuiltin_Setopt(t *testing.T) {
	// setopt should be recognized as a builtin
	if !isBuiltin("setopt") {
		t.Error("setopt should be a builtin")
	}
}
