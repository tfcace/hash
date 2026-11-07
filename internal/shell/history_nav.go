package shell

import "github.com/tfcace/hash/internal/history"

// historyNavLimit is how far back Up reaches.
const historyNavLimit = 1000

// historyNav is where Up/Down history browsing is: on the line being typed
// (pos 0, the zero value), or pos entries back with the typed line saved to
// come back to. Each editor session starts on the typed line.
type historyNav struct {
	pos   int    // entries back from the typed line; 0 is the typed line
	saved string // the typed line, kept while pos > 0
}

// reset starts a new editor session on the typed line.
func (h *historyNav) reset() {
	*h = historyNav{}
}

// move goes one entry older (dir -1, Up) or newer (dir +1, Down) through
// entries, newest first. It returns the line to show and whether to show it:
// false at either end, where the buffer stays as it is. Coming back to the
// typed line returns it, even when it was empty.
func (h *historyNav) move(dir int, current string, entries []history.Command) (line string, ok bool) {
	next := h.pos - dir
	if next < 0 || next > len(entries) {
		return "", false
	}
	if h.pos == 0 {
		h.saved = current
	}
	h.pos = next
	if i := next - 1; i >= 0 && i < len(entries) {
		return entries[i].Command, true
	}
	line, h.saved = h.saved, ""
	return line, true
}

// navigateHistory handles Up/Down arrow history navigation for the editor.
// dir=-1 for Up (older history), dir=+1 for Down (newer history).
// currentLine is the buffer, saved when browsing starts.
func (s *Shell) navigateHistory(dir int, currentLine string) (string, bool) {
	entries, _ := s.history.Search(history.SearchOptions{Limit: historyNavLimit})
	return s.historyNav.move(dir, currentLine, entries)
}
