package editor

import (
	"errors"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/termtext"
)

// The notice is bounded so it stays on the input line: by the cells it
// draws in. Bounding runes let a wide-character error take twice the room.
func TestGhostNoticeFor_BoundedByCells(t *testing.T) {
	for _, msg := range []string{strings.Repeat("x", 200), strings.Repeat("日", 200)} {
		notice := ghostNoticeFor(errors.New(msg))
		if w := termtext.Width(notice); w > ghostNoticeMaxWidth {
			t.Errorf("notice for %q... is %d cells, max %d", msg[:6], w, ghostNoticeMaxWidth)
		}
		if !strings.HasSuffix(notice, "…") {
			t.Errorf("a cut notice should end with an ellipsis: %q", notice)
		}
	}
}
