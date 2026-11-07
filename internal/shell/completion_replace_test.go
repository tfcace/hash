package shell

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/completion"
	"github.com/tfcace/hash/internal/editor"
)

// commandNameStub answers every word with the one command name grep.
type commandNameStub struct{}

func (commandNameStub) Name() string { return "stub" }
func (commandNameStub) Complete(context.Context, string, int) (completion.Result, error) {
	return completion.Result{Items: []completion.Item{{Value: "grep", Display: "grep"}}}, nil
}

// The router says where the completed word starts and the editor replaces
// from there: what the word is glued to (a pipe, an assignment's name)
// stays in the line when the user accepts the completion.
func TestCompletion_AcceptKeepsWhatTheWordIsGluedTo(t *testing.T) {
	tests := []struct {
		typed, want string
		completer   completion.Completer
		priority    completion.Priority
	}{
		{"ls|gr", "ls|grep", commandNameStub{}, completion.PriorityExecutable},
		{"PATH=$HO", "PATH=$HOME", completion.NewEnvCompleter(fixedEnv{"HOME=/h"}), completion.PriorityEnv},
		{"echo $((1 + $HO", "echo $((1 + $HOME", completion.NewEnvCompleter(fixedEnv{"HOME=/h"}), completion.PriorityEnv},
	}
	for _, tt := range tests {
		t.Run(tt.typed, func(t *testing.T) {
			router := completion.NewRouter()
			router.Register(tt.completer, tt.priority)
			cfg := editor.Config{CompleteOutcomeFunc: makeEditorCompleteOutcomeFunc(router)}
			in := strings.NewReader(tt.typed + "\t\r")
			result, err := editor.New(cfg, in, &bytes.Buffer{}).Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := strings.TrimSpace(result.Text)
			if got != tt.want {
				t.Fatalf("accepted line = %q, want %q", got, tt.want)
			}
		})
	}
}
