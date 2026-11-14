package prediction

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestStore_RecordAndGetSequence(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(filepath.Join(tmpDir, "prediction.db"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	defer store.Close()

	// Record a sequence
	err = store.RecordSequence("git pull", "npm test", "/project")
	if err != nil {
		t.Fatalf("RecordSequence error: %v", err)
	}

	// Record again to increase count
	store.RecordSequence("git pull", "npm test", "/project")

	// Get sequences
	seqs, err := store.RankSequences("git pull", "/project", 24, 10)
	if err != nil {
		t.Fatalf("RankSequences error: %v", err)
	}

	if len(seqs) != 1 {
		t.Fatalf("Expected 1 sequence, got %d", len(seqs))
	}
	if seqs[0].Count != 2 {
		t.Errorf("Count = %d, want 2", seqs[0].Count)
	}
}

// Two shells write the same prediction database at once; a writer waits for
// the other's lock instead of failing with "database is locked" (go-sqlite3
// sets a 5s busy timeout on every connection unless the DSN says otherwise).
func TestStore_ConcurrentWritersWait(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "predictions.db")
	var stores []*Store
	for range 2 {
		s, err := NewStore(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		stores = append(stores, s)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := stores[i%2]
			for j := range 50 {
				if err := s.RecordSequence(fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", j), "/x"); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write failed: %v", err)
	}
}
