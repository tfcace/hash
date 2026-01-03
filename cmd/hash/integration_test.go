//go:build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegration_LoginShellMarker(t *testing.T) {
	// Build hash
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test that -l flag sets HASH_LOGIN=1
	var stdout bytes.Buffer
	cmd = exec.Command(binPath, "-l", "-c", "echo $HASH_LOGIN")
	cmd.Stdout = &stdout
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		t.Fatalf("login shell failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "1" {
		t.Errorf("expected HASH_LOGIN=1, got '%s'", output)
	}
}

func TestIntegration_HashShellMarker(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test that HASH_SHELL=1 is always set
	var stdout bytes.Buffer
	cmd = exec.Command(binPath, "-c", "echo $HASH_SHELL")
	cmd.Stdout = &stdout
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		t.Fatalf("hash shell failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "1" {
		t.Errorf("expected HASH_SHELL=1, got '%s'", output)
	}
}

func TestIntegration_EnvPersistenceInSession(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test that exports persist across multiple -c invocations conceptually
	// by using a compound command
	var stdout bytes.Buffer
	cmd = exec.Command(binPath, "-c", "export FOO=bar; echo $FOO")
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		t.Fatalf("compound command failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "bar" {
		t.Errorf("expected 'bar', got '%s'", output)
	}
}

func TestIntegration_NonLoginInteractive(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "hash")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Test non-login shell doesn't set HASH_LOGIN
	var stdout bytes.Buffer
	cmd = exec.Command(binPath, "-c", "echo LOGIN=$HASH_LOGIN")
	cmd.Stdout = &stdout
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		t.Fatalf("non-login shell failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	// HASH_LOGIN should not be set (empty)
	if strings.Contains(output, "LOGIN=1") {
		t.Errorf("HASH_LOGIN should not be set for non-login shell, got: %s", output)
	}
}
