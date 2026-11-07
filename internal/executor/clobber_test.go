package executor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// `>|` forces an overwrite that noclobber would refuse. hash has no
// noclobber, so it is `>`: the interpreter used to report "unhandled
// redirect op" and write nothing.
func TestExecute_ClobberRedirectWrites(t *testing.T) {
	out := filepath.Join(t.TempDir(), "f")
	e := New()
	res, err := e.Execute(context.Background(), "echo hi >| "+out, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	b, rerr := os.ReadFile(out)
	if rerr != nil || string(b) != "hi\n" || res.ExitCode != 0 {
		t.Fatalf("file = %q (%v), status %d; want hi and 0", b, rerr, res.ExitCode)
	}
}
