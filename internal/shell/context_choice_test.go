package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	hashcontext "github.com/tfcace/hash/internal/context"
)

// The cwd picked with Ctrl+P is where the shell is at each request, not
// where it was when the picker ran.
func TestContextChoices_ResolveAfreshForEachRequest(t *testing.T) {
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("ok")}}}}
	sh, _, _ := newTurnShell(t, tr)
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })

	sh.contextPicks = pickOnly(t, sh, "cwd")

	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := ask(t, sh, "?? where am i"); err != nil {
		t.Fatal(err)
	}
	if got := tr.requests[0].Context.Cwd; got != dir {
		t.Errorf("request cwd = %q, want the cwd now %q", got, dir)
	}
}

func TestContextChoices_NothingPickedMeansDefaults(t *testing.T) {
	sh := &Shell{}
	if sh.contextSelection() != nil {
		t.Error("no picks should resolve to no selection")
	}
}

// pickOnly is the selection of confirming the picker with only the item
// with key checked (none for "").
func pickOnly(t *testing.T, sh *Shell, key string) hashcontext.Selection {
	t.Helper()
	c := sh.contextCollection()
	for i := range c.Items {
		c.Items[i].Selected = c.Items[i].Key == key
	}
	return hashcontext.SelectionOf(c)
}

// Picking nothing ("n for none") sends no picked context: not the
// auto-detected defaults, which is what an empty pick used to mean.
func TestContextChoices_NoneIsNotDefaults(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("ok")}}}}
	sh, _, _ := newTurnShell(t, tr)
	last := clipboard.NewBuffer(4)
	last.AddCommand("make")
	last.SetOutput("OUTPUT") // what the defaults would send as the last output
	sh.agentHandler.SetClipboard(last)
	sh.contextPicks = pickOnly(t, sh, "")
	if sh.contextSelection() == nil {
		t.Fatal("picking none resolved to the auto-detected defaults")
	}
	if err := ask(t, sh, "?? anything"); err != nil {
		t.Fatal(err)
	}
	if got := tr.requests[0].Context; got.GitBranch != "" || got.LastOutput != "" || len(got.History) != 0 {
		t.Errorf("request context = %+v, want nothing picked", got)
	}
}

// The picker opens on what was picked before, so Enter keeps it; it used to
// open on the defaults every time and Enter overwrote earlier picks.
func TestContextChoices_PickerOpensOnPicks(t *testing.T) {
	sh := &Shell{}
	sh.contextPicks = pickOnly(t, sh, "HOME")
	var got []string
	for _, it := range sh.pickedContext().SelectedItems() {
		got = append(got, it.Key)
	}
	if len(got) != 1 || got[0] != "HOME" {
		t.Fatalf("picker opens with %v selected, want [HOME]", got)
	}
}
