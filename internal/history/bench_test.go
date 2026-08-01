package history

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// benchVerbs give seeded commands realistic shared prefixes, so prefix
// searches see both dense ranges ("git c...") and sparse ones.
var benchVerbs = []string{
	"git status",
	"git checkout ",
	"git commit -m ",
	"go test ./internal/",
	"docker run --rm ",
	"ls -la ",
	"kubectl get pods -n ",
	"make ",
}

func benchCommand(i int) string {
	verb := benchVerbs[i%len(benchVerbs)]
	if verb == "git status" {
		return verb // A high-frequency exact repeat, like real history.
	}
	return fmt.Sprintf("%s%d", verb, i)
}

// seedStore fills a fresh store with total rows over roughly total/repeat
// distinct commands, timestamps ascending, with a mix of exit codes.
func seedStore(b *testing.B, total int) *Store {
	b.Helper()
	store, err := NewStore(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { store.Close() })

	tx, err := store.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO commands (command, exit_code, timestamp) VALUES (?, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	base := time.Now().Add(-time.Duration(total) * time.Second)
	for i := 0; i < total; i++ {
		exitCode := 0
		if i%7 == 3 {
			exitCode = 1
		}
		// Repeat each distinct command a few times at different timestamps.
		if _, err := stmt.Exec(benchCommand(i/4), exitCode, base.Add(time.Duration(i)*time.Second)); err != nil {
			b.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	// The background index load raced the direct SQL seeding above: wait it
	// out, then load again so the index reflects the seeded rows.
	store.waitPrefixIndex()
	store.loadPrefixIndex()
	return store
}

// BenchmarkSearchByPrefix measures the per-keystroke ghost-text lookup the
// editor suggestion function performs (limit 1, like makeEditorSuggestionFunc).
func BenchmarkSearchByPrefix(b *testing.B) {
	for _, rows := range []int{10_000, 100_000} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			store := seedStore(b, rows)
			prefixes := []struct{ name, prefix string }{
				{"dense", "git c"},
				{"narrow", "kubectl get pods"},
				{"miss", "zzz-no-such"},
			}
			for _, p := range prefixes {
				b.Run(p.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if _, err := store.SearchByPrefix(p.prefix, 1); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
