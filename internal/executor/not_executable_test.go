package executor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExecute_NonExecutableFileIsPermissionDenied126(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "deploy.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer

	result, err := exec.Execute(ctx, script, &stdout, &stderr)

	var ne *CommandNotExecutableError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want *CommandNotExecutableError", err)
	}
	if ne.Reason != "Permission denied" {
		t.Errorf("Reason = %q, want %q", ne.Reason, "Permission denied")
	}
	if result == nil || result.ExitCode != 126 {
		t.Errorf("result = %+v, want ExitCode 126", result)
	}
}

func TestExecute_DirectoryIsNotExecutable126(t *testing.T) {
	dir := t.TempDir()
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer

	result, err := exec.Execute(ctx, dir, &stdout, &stderr)

	var ne *CommandNotExecutableError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want *CommandNotExecutableError", err)
	}
	if ne.Reason != "Is a directory" {
		t.Errorf("Reason = %q, want %q", ne.Reason, "Is a directory")
	}
	if result == nil || result.ExitCode != 126 {
		t.Errorf("result = %+v, want ExitCode 126", result)
	}
}

func TestExecute_MissingPathStaysCommandNotFound127(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer

	result, err := exec.Execute(ctx, filepath.Join(t.TempDir(), "nope.sh"), &stdout, &stderr)

	if !IsCommandNotFound(err) {
		t.Fatalf("err = %v, want CommandNotFoundError", err)
	}
	if result == nil || result.ExitCode != 127 {
		t.Errorf("result = %+v, want ExitCode 127", result)
	}
}

// Relative paths are resolved against the interpreter's directory, not the
// process working directory.
func TestLookupFailure_RelativePathAgainstDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := lookupFailure(dir, "./deploy.sh")

	var ne *CommandNotExecutableError
	if !errors.As(err, &ne) || ne.Reason != "Permission denied" {
		t.Fatalf("lookupFailure(dir, ./deploy.sh) = %v, want permission denied", err)
	}
	if !IsCommandNotFound(lookupFailure(dir, "./missing.sh")) {
		t.Error("a missing relative path should stay command not found")
	}
	if !IsCommandNotFound(lookupFailure(dir, "deploy.sh")) {
		t.Error("a bare name is looked up on PATH, so it stays command not found")
	}
}
