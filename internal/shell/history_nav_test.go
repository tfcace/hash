package shell

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/history"
)

func newHistoryNavShell(t *testing.T, commands ...string) *Shell {
	t.Helper()
	store, err := history.NewStore(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, c := range commands {
		if _, err := store.Add(history.Command{Command: c}); err != nil {
			t.Fatal(err)
		}
	}
	sh := &Shell{history: store}
	sh.editorCfg.HistoryFunc = sh.navigateHistory
	return sh
}

// runEditorKeys types keys into an editor wired like the prompt's and
// returns the submitted line.
func runEditorKeys(t *testing.T, cfg editor.Config, keys ...[]byte) string {
	t.Helper()
	in := bytes.NewReader(bytes.Join(append(keys, []byte{0x1b, '\r'}), nil))
	result, err := editor.New(cfg, in, &bytes.Buffer{}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result.Text
}

var (
	keyUp   = []byte{0x1b, '[', 'A'}
	keyDown = []byte{0x1b, '[', 'B'}
)

// Up then Down from an empty prompt comes back to the empty line it left,
// not the entry Up recalled.
func TestHistoryNav_DownRestoresAnEmptyLine(t *testing.T) {
	sh := newHistoryNavShell(t, "ls -la")
	if got := runEditorKeys(t, sh.editorCfg, keyUp, keyDown); got != "" {
		t.Errorf("Up, Down from an empty prompt submitted %q, want the empty line back", got)
	}
}

func TestHistoryNav_MoveWalksEntriesAndStopsAtTheEnds(t *testing.T) {
	entries := []history.Command{{Command: "newest"}, {Command: "older"}} // newest first
	var h historyNav
	step := func(dir int, current, want string, wantOK bool) {
		t.Helper()
		got, ok := h.move(dir, current, entries)
		if got != want || ok != wantOK {
			t.Fatalf("move(%d, %q) = %q, %v; want %q, %v", dir, current, got, ok, want, wantOK)
		}
	}
	step(+1, "typed", "", false) // Down on the typed line: nothing newer
	step(-1, "typed", "newest", true)
	step(-1, "newest", "older", true)
	step(-1, "older", "", false) // Up past the oldest: stays
	step(+1, "older", "newest", true)
	step(+1, "newest", "typed", true) // back to what was typed
	step(+1, "typed", "", false)
	if h != (historyNav{}) {
		t.Errorf("back on the typed line, nav = %+v; want the zero value", h)
	}
}

func TestHistoryNav_EmptyHistoryHasNothingToShow(t *testing.T) {
	var h historyNav
	if got, ok := h.move(-1, "x", nil); ok || got != "" {
		t.Errorf("move on empty history = %q, %v; want nothing", got, ok)
	}
}

func TestHistoryNav_ResetStartsOnTheTypedLine(t *testing.T) {
	h := historyNav{pos: 2, saved: "draft"}
	h.reset()
	if got, ok := h.move(-1, "", []history.Command{{Command: "newest"}}); got != "newest" || !ok {
		t.Errorf("first Up after reset = %q, %v; want the newest entry", got, ok)
	}
}

// The conversation's reply box offers history too; it starts from the
// newest entry, not wherever Up left the prompt before the ?? ran.
func TestReadAgentConversationReply_StartsHistoryFromTheNewest(t *testing.T) {
	sh := newHistoryNavShell(t, "oldest", "older", "newest")
	sh.navigateHistory(-1, "")
	sh.navigateHistory(-1, "newest") // the prompt was left two back
	var typed *os.File
	sh.editorCfg.Input, typed = typedKeys(t)
	typed.WriteString("\x1b[A\r") // Up, Enter
	var reply string
	var ok bool
	captureStdout(t, sh, func() { reply, ok = sh.readAgentConversationReply(context.Background(), true) })
	entries, _ := sh.history.Search(history.SearchOptions{Limit: historyNavLimit})
	if want := entries[0].Command; !ok || reply != want {
		t.Errorf("Up in the reply editor recalled %q, %v; want the newest entry %q", reply, ok, want)
	}
}
