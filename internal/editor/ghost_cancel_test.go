package editor

import "testing"

// streamWithCancel attaches a stream whose request cancel is counted.
func streamWithCancel(t *testing.T, prefix string) (*Editor, *int) {
	t.Helper()
	ed := newKeyEditor(t, Config{}, prefix)
	canceled := 0
	ed.SetGhostTextStreaming(make(chan GhostStreamUpdate, 4), make(chan error, 1), func() { canceled++ })
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: "log"}, true)
	return ed, &canceled
}

// Leaving the inline fill while it streams (Esc, typing over it, Ctrl+C)
// cancels the agent request: it used to run on unseen, holding the agent.
func TestGhost_DetachingCancelsTheRequest(t *testing.T) {
	for name, key := range map[string]Key{
		"Esc":    {Special: KeyEscape},
		"typing": {Rune: 'x'},
		"Ctrl+C": {Rune: 'c', Ctrl: true},
	} {
		ed, canceled := streamWithCancel(t, "git ")
		ed.handleKeyEvent(key)
		if *canceled != 1 {
			t.Errorf("%s: request canceled %d times, want once", name, *canceled)
		}
		if ed.ghost.streaming() {
			t.Errorf("%s: still streaming", name)
		}
	}
}

// Accepting what arrived keeps the stream, and the request with it.
func TestGhost_AcceptingMidStreamKeepsTheRequest(t *testing.T) {
	ed, canceled := streamWithCancel(t, "git ")
	press(t, ed, keyTab())
	press(t, ed, Key{Special: KeyTab, Alt: true})
	if *canceled != 0 || !ed.ghost.streaming() {
		t.Fatalf("canceled=%d streaming=%v, want the request kept", *canceled, ed.ghost.streaming())
	}
}

// After Tab took part of a fill that still streams, Enter runs what was
// taken and drops the rest (canceling the request). It used to be ignored
// until the stream ended.
func TestGhost_EnterRunsTheAcceptedPartMidStream(t *testing.T) {
	ed, canceled := streamWithCancel(t, "git ")
	press(t, ed, keyTab())
	res, done := press(t, ed, Key{Special: KeyEnter})
	if !done || res.Text != "git log" || res.Exit != ExitSubmit {
		t.Fatalf("Enter = (%+v, %v), want submit of %q", res, done, "git log")
	}
	if *canceled != 1 {
		t.Errorf("request canceled %d times, want once", *canceled)
	}
}

// With nothing taken yet, Enter mid-stream still waits: it would run the
// bare line the fill is completing.
func TestGhost_EnterBeforeAnyAcceptStillWaits(t *testing.T) {
	ed, canceled := streamWithCancel(t, "git ")
	if _, done := press(t, ed, Key{Special: KeyEnter}); done {
		t.Fatal("Enter ran the bare line while the fill streams")
	}
	if *canceled != 0 || !ed.ghost.streaming() {
		t.Fatalf("canceled=%d streaming=%v, want the stream kept", *canceled, ed.ghost.streaming())
	}
}
