package shell

import (
	"testing"

	"github.com/tfcace/hash/internal/prompt"
)

// A prompt with no background chip yields the default primary but still has
// its own pass and fail colors: the interface takes them.
func TestAdoptPaletteKeepsAChiplessPromptsColors(t *testing.T) {
	extracted := prompt.DefaultPalette()
	extracted.Success, extracted.Error = "2", "1"
	s := &Shell{colorPalette: prompt.DefaultPalette()}

	s.adoptPalette(extracted)

	if s.colorPalette.Success != "2" || s.colorPalette.Error != "1" {
		t.Errorf("got success %q error %q, want the prompt's own 2 and 1", s.colorPalette.Success, s.colorPalette.Error)
	}
}

func TestAdoptPaletteTakesAnExtractedPrimary(t *testing.T) {
	extracted := prompt.DefaultPalette()
	extracted.Primary = "#C678DD"
	s := &Shell{colorPalette: prompt.DefaultPalette()}

	s.adoptPalette(extracted)

	if s.colorPalette != extracted || s.editorCfg.ScrollbarColor != "#C678DD" {
		t.Errorf("palette %+v, scrollbar %q", s.colorPalette, s.editorCfg.ScrollbarColor)
	}
}

func TestAdoptPaletteIgnoresAPromptThatShowedNoColors(t *testing.T) {
	s := &Shell{colorPalette: prompt.Palette{Primary: "#111111"}}
	s.adoptPalette(prompt.DefaultPalette())
	if s.colorPalette.Primary != "#111111" {
		t.Errorf("palette replaced by the default: %+v", s.colorPalette)
	}
}
