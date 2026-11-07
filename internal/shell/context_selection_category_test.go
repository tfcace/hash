package shell

import (
	"testing"

	hashcontext "github.com/tfcace/hash/internal/context"
)

// A history item's Key is the whole command, so a command named like a fact
// ("git branch") must still go to History, never to the fact's field.
func TestBuildContextFromSelection_HistoryNamedLikeFact(t *testing.T) {
	c := hashcontext.NewBuilder().WithHistory([]string{"git branch", "kube context", "cwd"}).Build()
	for i := range c.Items {
		c.Items[i].Selected = true
	}
	ctx := buildContextFromSelection(c)
	if ctx.GitBranch != "" || ctx.KubeContext != "" {
		t.Errorf("GitBranch = %q, KubeContext = %q: history commands set facts", ctx.GitBranch, ctx.KubeContext)
	}
	if len(ctx.History) != 3 {
		t.Errorf("History = %q, want all three commands", ctx.History)
	}
}

func TestBuildContextFromSelection_FactsStillApply(t *testing.T) {
	c := hashcontext.NewCollection()
	c.Add(hashcontext.Item{Category: hashcontext.CategoryAutoDetect, Key: "git branch", Value: "main", Selected: true})
	c.Add(hashcontext.Item{Category: hashcontext.CategoryAutoDetect, Key: "cwd", Value: "/tmp/x", Selected: true})
	c.Add(hashcontext.Item{Category: hashcontext.CategoryEnv, Key: "HOME", Value: "/home/u", Selected: true})
	ctx := buildContextFromSelection(c)
	if ctx.GitBranch != "main" || ctx.Cwd != "/tmp/x" || ctx.EnvVars["HOME"] != "/home/u" {
		t.Errorf("facts not applied: %+v", ctx)
	}
}
