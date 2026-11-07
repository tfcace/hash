package shell

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/executor"
)

// The status of a line the interpreter did not run (a ?? request, a hash
// builtin, a refused line) becomes $? without running a command for it:
// the old `(exit N)` fired the user's ERR trap for every failed ?? request.
func TestSyncStatus_DoesNotFireTheERRTrap(t *testing.T) {
	fired := filepath.Join(t.TempDir(), "fired")
	s := &Shell{executor: executor.New()}
	if _, err := s.executor.Execute(context.Background(), `trap 'echo fired >> `+fired+`' ERR`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	s.syncStatus(3)
	if _, err := os.Stat(fired); err == nil {
		t.Fatal("syncStatus fired the ERR trap")
	}
	var out bytes.Buffer
	if _, err := s.executor.Execute(context.Background(), `echo $?`, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if out.String() != "3\n" {
		t.Fatalf("$? = %q, want 3", out.String())
	}
}
