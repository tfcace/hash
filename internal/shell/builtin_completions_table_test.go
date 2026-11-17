package shell

import (
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/completion"
)

var listedPlugins = []completion.PluginInfo{
	{Command: "docker", SpecName: "docker", Builtin: true, Rules: 12},
	{Command: "kubectl", SpecName: "k8s", Rules: 1},
}

// The list is a table like the ones a reply draws: the same header, rule
// and columns, with the count right-aligned.
func TestCompletionsTable_DrawsThePluginsAsATable(t *testing.T) {
	want := "Command  Spec    Origin    Rules\n" +
		"───────  ──────  ────────  ─────\n" +
		"docker   docker  built-in     12\n" +
		"kubectl  k8s     user          1"
	if got := stripANSI(completionsTable(listedPlugins, 80)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCompletionsTable_FitsANarrowPane(t *testing.T) {
	got := completionsTable(listedPlugins, 24)
	assertRowsFit(t, got, 25)
	if !strings.Contains(stripANSI(got), "Command: kubectl") {
		t.Errorf("a pane too narrow for the columns should list records:\n%s", stripANSI(got))
	}
}

// A user's spec names its own commands: what it wrote is drawn inert.
func TestCompletionsTable_DrawsSpecTextInert(t *testing.T) {
	got := completionsTable([]completion.PluginInfo{{Command: "x\x1b]0;pwned\x07", SpecName: "s", Rules: 1}}, 80)
	if strings.Contains(got, "\x1b]0;pwned") || strings.Contains(got, "\x07") {
		t.Errorf("spec text is drawn raw: %q", got)
	}
}
