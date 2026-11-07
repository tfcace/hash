package history

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Ctrl+R's unique search found each row's later runs by scanning every run
// of its command, which is quadratic in a command's run count: 20k runs of
// 5 commands took seconds. Each command's latest run is found through an
// index now.
func TestSearch_UniqueIsFastForACommandRunThousandsOfTimes(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	const total, commands = 20000, 5
	base := time.Now().Add(-total * time.Second)
	for i := range total {
		cmd := fmt.Sprintf("git status %d", i%commands)
		if _, err := tx.Exec(`INSERT INTO commands (command, cwd, timestamp) VALUES (?, '/', ?)`,
			cmd, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"", "git", "status 3"} {
		start := time.Now()
		got, err := store.Search(SearchOptions{Query: query, Limit: searchLimit, Unique: true})
		took := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		want := commands
		if query == "status 3" {
			want = 1
		}
		if len(got) != want {
			t.Fatalf("Search(%q) = %d rows, want %d", query, len(got), want)
		}
		for _, c := range got {
			// The latest run of "git status k" is row total-commands+k+1.
			var k int
			fmt.Sscanf(c.Command, "git status %d", &k)
			if wantID := int64(total - commands + k + 1); c.ID != wantID {
				t.Errorf("Search(%q): %q at id %d, want its latest run, id %d", query, c.Command, c.ID, wantID)
			}
		}
		if took > 50*time.Millisecond {
			t.Errorf("Search(%q, unique) took %v, want under 50ms", query, took)
		}
	}
}
