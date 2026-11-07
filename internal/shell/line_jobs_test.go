package shell

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/executor"
)

func TestExecuteLine_EvalDefinedFunctionsJobOutlivesItsLine(t *testing.T) {
	t.Parallel()
	sh := &Shell{executor: executor.New()}
	out := filepath.Join(t.TempDir(), "f")
	sh.executor.SetExportedEnv("OUT", out)
	ctx := context.Background()
	if _, err := sh.executeLine(ctx, `eval 'g() { { sleep 0.2; echo hi > "$OUT"; } & }'`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := sh.executeLine(ctx, "g", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(out); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("an eval-defined function's job was cut off with its line")
}

func TestExecuteLine_AFunctionCallingLineKeepsNothing(t *testing.T) {
	sh := &Shell{executor: executor.New()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := sh.executeLine(ctx, "f() { :; }", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		sh.executeLine(ctx, "f", io.Discard, io.Discard)
	}
	before := heapInUse()
	const n = 20000
	for i := 0; i < n; i++ {
		sh.executeLine(ctx, "f", io.Discard, io.Discard)
	}
	if grew := int64(heapInUse()) - int64(before); grew > 20*n {
		t.Errorf("heap grew %d B over %d function-calling lines (%d B/line)", grew, n, grew/n)
	}
}
