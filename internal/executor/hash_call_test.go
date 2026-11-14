package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// hash migrate, typed in hash, runs hash's own migrate (the executable
// hash runs as), with its arguments: the interpreter's hash builtin took
// it, and it did nothing.
func TestExecute_HashSubcommandRunsHash(t *testing.T) {
	e := New()
	self := filepath.Join(t.TempDir(), "hash")
	if err := os.WriteFile(self, []byte("#!/bin/sh\necho \"self: $*\"\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.shellPath = self
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, cmd := range []string{"hash migrate", "hash migrate status"} {
		var stdout bytes.Buffer
		res, err := e.Execute(ctx, cmd, &stdout, &stdout)
		if err != nil {
			t.Fatalf("Execute(%q): %v", cmd, err)
		}
		if want := "self: " + cmd[len("hash "):] + "\n"; stdout.String() != want || res.ExitCode != 3 {
			t.Errorf("%q: output %q status %d, want %q and hash's status 3", cmd, stdout.String(), res.ExitCode, want)
		}
	}
}

// hash NAME of a command that is not a builtin, a function or in PATH
// fails as bash's hash builtin does; the interpreter's took any name.
func TestExecute_HashOfAnUnknownNameFails(t *testing.T) {
	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, tt := range []struct {
		cmd, stderr string
		status      int
	}{
		{"hash hash_t_nosuch", "hash: hash: hash_t_nosuch: not found\n", 1},
		{"hash ls hash_t_nosuch cd", "hash: hash: hash_t_nosuch: not found\n", 1},
		{"hash -r hash_t_nosuch", "hash: hash: hash_t_nosuch: not found\n", 1},
		{"f() { :; }; hash ls cd f /no/such", "", 0},
		{"hash -p /bin/ls hash_t_nosuch", "", 0},
	} {
		var stdout, stderr bytes.Buffer
		res, err := e.Execute(ctx, tt.cmd, &stdout, &stderr)
		if err != nil {
			t.Fatalf("Execute(%q): %v", tt.cmd, err)
		}
		if stderr.String() != tt.stderr || stdout.String() != "" || res.ExitCode != tt.status {
			t.Errorf("%q: stdout %q stderr %q status %d, want stderr %q status %d", tt.cmd, stdout.String(), stderr.String(),
				res.ExitCode, tt.stderr, tt.status)
		}
	}
}
