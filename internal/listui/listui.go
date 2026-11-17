// Package listui holds what hash's list screens (history search, the
// context picker) agree on, so they behave as one.
package listui

// FrameRows is how many rows a list screen's frame may take in a pane
// height rows tall: two fifths of it, so the frame sits by the prompt and
// leaves the scrollback in view, but room for a useful list in a short
// pane, and never the prompt's own row. 0 is no limit: the height is not
// known.
func FrameRows(height int) int {
	if height <= 0 {
		return 0
	}
	return min(max(height*2/5, 12), height-1)
}

// Window is the part of list a frame has room rows for (at least three),
// around the cursor's row. When rows are hidden above or below, the first
// or last row shown is replaced by more's count of them, that row included.
func Window(list []string, cursor, room int, more func(hidden int, above bool) string) []string {
	room = max(room, 3)
	if len(list) <= room {
		return list
	}
	start := min(max(cursor-room/2, 0), len(list)-room)
	shown := append([]string(nil), list[start:start+room]...)
	if start > 0 {
		shown[0] = more(start+1, true)
	}
	if hidden := len(list) - start - room; hidden > 0 {
		shown[room-1] = more(hidden+1, false)
	}
	return shown
}

// Page is how many rows a page key moves the cursor in a pane height rows
// tall: about what the frame lists, less a row kept for context.
func Page(height int) int {
	if limit := FrameRows(height); limit > 0 {
		return max(limit-5, 3)
	}
	return 10
}
