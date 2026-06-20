package onboarding

import (
	"errors"
	"testing"
)

func lookOnly(present ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(cmd string) (string, error) {
		if set[cmd] {
			return "/usr/local/bin/" + cmd, nil
		}
		return "", errors.New("not found")
	}
}

func TestDetectReturnsOnlyAgentsFoundOnPath(t *testing.T) {
	got := Detect(lookOnly("claude-agent-acp"))
	if len(got) != 1 {
		t.Fatalf("Detect found %d agents, want 1: %+v", len(got), got)
	}
	if got[0].Command != "claude-agent-acp" {
		t.Errorf("found %q, want claude-agent-acp", got[0].Command)
	}
}

func TestDetectNoneFound(t *testing.T) {
	if got := Detect(lookOnly()); len(got) != 0 {
		t.Fatalf("Detect found %d agents on empty PATH, want 0", len(got))
	}
}

func TestDetectAllFound(t *testing.T) {
	got := Detect(lookOnly("claude-agent-acp", "gemini"))
	if len(got) != len(Known) {
		t.Fatalf("Detect found %d, want all %d", len(got), len(Known))
	}
}

func TestGeminiCarriesACPArg(t *testing.T) {
	var gemini *Agent
	for i := range Known {
		if Known[i].Command == "gemini" {
			gemini = &Known[i]
		}
	}
	if gemini == nil {
		t.Fatal("no gemini agent in Known")
	}
	if len(gemini.Args) != 1 || gemini.Args[0] != "--experimental-acp" {
		t.Errorf("gemini args = %v, want [--experimental-acp]", gemini.Args)
	}
}
