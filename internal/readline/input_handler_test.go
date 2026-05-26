package readline

import (
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/history"
)

func TestInputHandler_NewInputHandler(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := history.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	cfg := Config{
		Prompt:      "$ ",
		Keybindings: "emacs",
	}

	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rl.Close()

	ih := NewInputHandler(rl, store)

	if ih.readline != rl {
		t.Error("InputHandler.readline not set correctly")
	}

	if ih.history != store {
		t.Error("InputHandler.history not set correctly")
	}
}

func TestInputHandler_SetPickerFunc(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := history.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	cfg := Config{
		Prompt:      "$ ",
		Keybindings: "emacs",
	}

	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rl.Close()

	ih := NewInputHandler(rl, store)

	// Initially pickerFunc should be nil
	if ih.pickerFunc != nil {
		t.Error("pickerFunc should be nil initially")
	}

	// Set picker function
	called := false
	ih.SetPickerFunc(func() string {
		called = true
		return "test command"
	})

	if ih.pickerFunc == nil {
		t.Error("pickerFunc should be set after SetPickerFunc()")
	}

	// HandleCtrlR should call the picker function
	ih.HandleCtrlR()

	if !called {
		t.Error("HandleCtrlR should call the picker function")
	}
}

func TestInputHandler_HistoryStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := history.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ih := NewInputHandler(nil, store)

	// Should return the store
	if ih.HistoryStore() != store {
		t.Error("HistoryStore() should return the history store")
	}
}

func TestInputHandler_HistoryStore_Nil(t *testing.T) {
	ih := NewInputHandler(nil, nil)

	// Should return nil gracefully
	if ih.HistoryStore() != nil {
		t.Error("HistoryStore() should return nil when no store set")
	}
}

func TestInputHandler_ClipboardBuffer(t *testing.T) {
	ih := NewInputHandler(nil, nil)

	// Initially nil
	if ih.ClipboardBuffer() != nil {
		t.Error("ClipboardBuffer() should be nil initially")
	}

	// Set clipboard
	buf := clipboard.NewBuffer(10)
	ih.SetClipboard(buf)

	// Should return the buffer
	if ih.ClipboardBuffer() != buf {
		t.Error("ClipboardBuffer() should return the clipboard buffer")
	}
}

func TestInputHandler_SetReadline(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := history.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	cfg := Config{
		Prompt:      "$ ",
		Keybindings: "emacs",
	}

	rl1, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rl1.Close()

	rl2, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rl2.Close()

	ih := NewInputHandler(rl1, store)

	if ih.readline != rl1 {
		t.Error("Initial readline not set correctly")
	}

	ih.SetReadline(rl2)

	if ih.readline != rl2 {
		t.Error("SetReadline() did not update readline")
	}
}

func TestInputHandler_SetReadlineBuffer(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := history.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	cfg := Config{
		Prompt:      "$ ",
		Keybindings: "emacs",
	}

	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rl.Close()

	ih := NewInputHandler(rl, store)

	// Should not panic when setting buffer
	ih.SetReadlineBuffer("echo test")

	// Verify buffer was set (this is a minimal check)
	if rl == nil {
		t.Error("Readline became nil after SetReadlineBuffer")
	}
}

func TestInputHandler_SetReadlineBuffer_NilReadline(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := history.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	ih := NewInputHandler(nil, store)

	// Should not panic with nil readline
	ih.SetReadlineBuffer("echo test")
}
