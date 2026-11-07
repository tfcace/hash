package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/compat"
)

// A startup file that cannot be read is named once, as configured: the
// error from os.Stat names it again with its full path.
func TestStartupFileError_NamesTheFileOnce(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, ".hashrc")
	if err := os.WriteFile(file, []byte("echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err := startupFile(file)
	if err == nil {
		t.Fatal("startupFile of an unreadable file: nil error")
	}
	msg := compat.FileError(file, err).Error()
	if strings.Count(msg, ".hashrc") != 1 || !strings.HasSuffix(msg, ": permission denied") {
		t.Errorf("message = %q, want the file named once", msg)
	}
}
