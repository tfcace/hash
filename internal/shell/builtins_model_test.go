package shell

import (
	"testing"

	"github.com/tfcace/hash/internal/agent"
)

func sampleModels() []agent.ModelOption {
	return []agent.ModelOption{
		{Value: "default", Name: "Default (recommended)"},
		{Value: "sonnet", Name: "Sonnet"},
		{Value: "haiku", Name: "Haiku"},
	}
}

func TestResolveModel(t *testing.T) {
	models := sampleModels()
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"sonnet", "sonnet", true},
		{"Sonnet", "sonnet", true},  // by display name
		{"SONNET", "sonnet", true},  // case-insensitive value
		{"  haiku ", "haiku", true}, // trimmed
		{"Default (recommended)", "default", true},
		{"opus", "", false},
	}
	for _, tt := range tests {
		got, ok := resolveModel(models, tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("resolveModel(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestModelDisplayName(t *testing.T) {
	models := sampleModels()
	if got := modelDisplayName(models, "sonnet"); got != "Sonnet" {
		t.Errorf("modelDisplayName(sonnet) = %q, want Sonnet", got)
	}
	if got := modelDisplayName(models, "unknown"); got != "unknown" {
		t.Errorf("modelDisplayName(unknown) = %q, want unknown (fallback)", got)
	}
}

func TestIsListArg(t *testing.T) {
	for _, ok := range []string{"--list", "-l", "list"} {
		if !isListArg(ok) {
			t.Errorf("isListArg(%q) = false, want true", ok)
		}
	}
	for _, no := range []string{"sonnet", "", "--help"} {
		if isListArg(no) {
			t.Errorf("isListArg(%q) = true, want false", no)
		}
	}
}

func TestModelBuiltinRegistered(t *testing.T) {
	if !isBuiltin("model") {
		t.Fatal("isBuiltin(model) = false, want true")
	}
}
