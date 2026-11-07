package editor

import (
	"bytes"
	"errors"
	"testing"
)

// A claim's release frees that claim and no other. The prompt takes back a
// leaked claim (Reset); when the leaked holder lets go late, its release
// used to clear whoever held the keys by then, so a second reader could
// claim them beside the editor.
func TestInputReader_StaleReleaseFreesOnlyItsOwnClaim(t *testing.T) {
	r := NewInputReader(&bytes.Buffer{})
	leaked, err := r.Claim("turn watcher")
	if err != nil {
		t.Fatal(err)
	}
	if was := r.Reset(); was != "turn watcher" {
		t.Errorf("Reset took the keys from %q, want the turn watcher", was)
	}
	release, err := r.Claim("editor")
	if err != nil {
		t.Fatal(err)
	}
	leaked() // the leaked holder lets go late
	if owner := r.Owner(); owner != "editor" {
		t.Errorf("after a stale release the owner is %q, want the editor", owner)
	}
	if _, err := r.Claim("question"); !errors.Is(err, ErrInputBusy) {
		t.Errorf("a second reader claimed the keys while the editor reads them: %v", err)
	}

	release()
	release() // repeating is safe
	if owner := r.Owner(); owner != "" {
		t.Errorf("after the editor's release the owner is %q, want none", owner)
	}
	again, err := r.Claim("question")
	if err != nil {
		t.Fatalf("the keys stayed claimed after release: %v", err)
	}
	release() // a release repeated after a new claim leaves it alone
	if owner := r.Owner(); owner != "question" {
		t.Errorf("a repeated release freed the next claim: owner %q", owner)
	}
	again()
}
