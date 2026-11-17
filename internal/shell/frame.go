package shell

// Every surface hash draws in the scrollback (a banner, a prompt, a block in
// a reply) is the same frame: a title row, body rows behind a bar, and a
// last row of the keys that act on it.
//
//	┌ title · summary
//	│ body
//	└─ enter to run · esc to dismiss
const frameGray = "\x1b[90m"

// frameTop is a frame's first row: its title, bold in color (an escape
// sequence, or "" for none), and a summary when there is one.
func frameTop(title, summary, color string) string {
	row := frameGray + "┌ " + ansiReset + ansiBold + color + title + ansiReset
	if summary != "" {
		row += frameGray + " · " + ansiReset + summary
	}
	return row
}

// frameRow is a body row.
func frameRow(body string) string {
	return frameGray + "│" + ansiReset + " " + body
}

// frameFoot is a frame's last row: the keys that act on it, the whole ones
// that fit the pane.
func frameFoot(hints ...string) string {
	return frameGray + fitHint("└─ ", "", hints, terminalColumns()) + ansiReset
}
