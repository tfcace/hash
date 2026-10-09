package shell

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/markdown"
)

// The sanitizer holds back a tail of bytes so a legacy marker split across
// chunks can still be removed. That cut must never land inside a multibyte
// character: each emitted piece is rendered on its own, and a partial
// character turns into replacement glyphs on screen.
func TestLegacyAgentMarkerSanitizer_NeverSplitsRunes(t *testing.T) {
	s := newLegacyAgentMarkerSanitizer()
	// 20 bytes: the 15-byte hold-back puts the cut inside the em dash.
	first := "ref — general form"
	if len(first) != 20 {
		t.Fatalf("test input is %d bytes, want 20", len(first))
	}

	pieces := []string{s.Write(first), s.Write(" continues\n"), s.Flush()}

	for i, p := range pieces {
		if !utf8.ValidString(p) {
			t.Errorf("piece %d is not valid UTF-8: %q", i, p)
		}
	}
	r := markdown.NewStreamingRenderer()
	var rendered strings.Builder
	for _, p := range pieces {
		rendered.WriteString(r.Write(p))
	}
	rendered.WriteString(r.Finish())
	if strings.Contains(rendered.String(), "�") || !strings.Contains(rendered.String(), "—") {
		t.Errorf("em dash did not survive streaming, rendered: %q", rendered.String())
	}
}
