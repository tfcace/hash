package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore_All_OldestFirst(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Now().Add(-time.Hour)
	for i, c := range []string{"first", "second", "third"} {
		if _, err := store.Add(Command{Command: c, Timestamp: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Command != "first" || all[2].Command != "third" {
		t.Errorf("All() = %+v, want first, second, third in order", all)
	}
}
