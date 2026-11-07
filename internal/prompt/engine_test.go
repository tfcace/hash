package prompt

import "testing"

func TestGenerate_ModeNoneIsBare(t *testing.T) {
	p := New(Config{Mode: "none"})
	if got := p.Generate(PromptContext{Cwd: "/home/user/projects"}); got != "❯ " {
		t.Errorf("Generate() = %q, want a bare prompt character", got)
	}
}

func TestEngineForMode(t *testing.T) {
	tests := map[string]engine{
		"starship": engineStarship,
		"built-in": engineBuiltin,
		"none":     engineNone,
		"":         engineStarship,
		"bogus":    engineBuiltin,
	}
	for mode, want := range tests {
		if got := engineForMode(mode); got != want {
			t.Errorf("engineForMode(%q) = %v, want %v", mode, got, want)
		}
	}
}
