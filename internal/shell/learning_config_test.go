package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/config"
)

func TestOpenLearningStore_DisabledDoesNotCreateDatabase(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)
	cfg := config.Default()
	cfg.Learning.Enabled = false

	store, err := openLearningStore(cfg.Learning.Enabled)
	if err != nil {
		t.Fatalf("openLearningStore() error = %v", err)
	}
	if store != nil {
		store.Close()
		t.Fatal("openLearningStore() returned a store while learning is disabled")
	}

	dbPath := filepath.Join(dataRoot, "hash", "learning.db")
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("learning database exists while disabled: os.Stat() error = %v", err)
	}
}
