package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain_CFlag(t *testing.T) {
	// Build the binary in a temp location
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash_test")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test -c flag
	var stdout, stderr bytes.Buffer
	cmd = exec.Command(binPath, "-c", "echo hello")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		t.Fatalf("hash -c failed: %v, stderr: %s", err, stderr.String())
	}

	output := strings.TrimSpace(stdout.String())
	if output != "hello" {
		t.Errorf("expected 'hello', got '%s'", output)
	}
}

func TestMain_CFlag_ExitCode(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash_test")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test exit code propagation
	cmd = exec.Command(binPath, "-c", "exit 42")
	err := cmd.Run()

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 42 {
			t.Errorf("expected exit code 42, got %d", exitErr.ExitCode())
		}
	} else if err != nil {
		t.Fatalf("unexpected error type: %v", err)
	} else {
		t.Error("expected non-zero exit code")
	}
}

func TestMain_CFlag_WithEnv(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash_test")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test that HASH_SHELL is set
	var stdout bytes.Buffer
	cmd = exec.Command(binPath, "-c", "echo $HASH_SHELL")
	cmd.Stdout = &stdout
	cmd.Env = os.Environ()

	err := cmd.Run()
	if err != nil {
		t.Fatalf("hash -c failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "1" {
		t.Errorf("expected HASH_SHELL=1, got '%s'", output)
	}
}

func TestMain_CFlag_NoArg(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash_test")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test -c without argument
	cmd = exec.Command(binPath, "-c")
	err := cmd.Run()

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 2 {
			t.Errorf("expected exit code 2, got %d", exitErr.ExitCode())
		}
	} else if err == nil {
		t.Error("expected non-zero exit code for -c without argument")
	} else {
		t.Fatalf("unexpected error type: %v", err)
	}
}

func TestMain_LoginFlag(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash_test")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test -l flag sets HASH_LOGIN=1
	var stdout bytes.Buffer
	cmd = exec.Command(binPath, "-l", "-c", "echo $HASH_LOGIN")
	cmd.Stdout = &stdout
	cmd.Env = os.Environ()

	err := cmd.Run()
	if err != nil {
		t.Fatalf("hash -l -c failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "1" {
		t.Errorf("expected HASH_LOGIN=1, got '%s'", output)
	}
}

func TestMain_LoginFlag_Long(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash_test")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test --login flag
	var stdout bytes.Buffer
	cmd = exec.Command(binPath, "--login", "-c", "echo $HASH_LOGIN")
	cmd.Stdout = &stdout
	cmd.Env = os.Environ()

	err := cmd.Run()
	if err != nil {
		t.Fatalf("hash --login -c failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "1" {
		t.Errorf("expected HASH_LOGIN=1, got '%s'", output)
	}
}

func TestMain_Argv0Login(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash_test")
	loginBinPath := filepath.Join(tmpDir, "-hash")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Create symlink with leading dash
	if err := os.Symlink(binPath, loginBinPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Test argv[0] with leading - sets login mode
	var stdout bytes.Buffer
	cmd = exec.Command(loginBinPath, "-c", "echo $HASH_LOGIN")
	cmd.Stdout = &stdout
	cmd.Env = os.Environ()

	err := cmd.Run()
	if err != nil {
		t.Fatalf("hash via -hash symlink failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "1" {
		t.Errorf("expected HASH_LOGIN=1 via argv[0], got '%s'", output)
	}
}

func TestMain_CFlag_PositionalArgs(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash_test")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test: hash -c 'echo $0 $1 $2' myarg0 arg1 arg2
	var stdout bytes.Buffer
	cmd = exec.Command(binPath, "-c", "echo $0 $1 $2", "myarg0", "arg1", "arg2")
	cmd.Stdout = &stdout
	cmd.Env = os.Environ()

	err := cmd.Run()
	if err != nil {
		t.Fatalf("hash -c with args failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	expected := "myarg0 arg1 arg2"
	if output != expected {
		t.Errorf("expected '%s', got '%s'", expected, output)
	}
}
