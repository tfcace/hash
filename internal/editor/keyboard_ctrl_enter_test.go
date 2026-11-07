package editor

import "testing"

// With kitty's disambiguate flag or modifyOtherKeys, Ctrl+Enter and
// Ctrl+Escape arrive as CSI sequences that keep the Ctrl; legacy terminals
// send the plain key. Nothing binds the modified forms, so the editor acts
// on them as the plain key: Ctrl+Enter used to do nothing.
func TestEditor_CtrlEnterAndCtrlEscapeActPlain(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "echo hi")
	if res, done := press(t, ed, ParseKey([]byte("\x1b[13;5u"))); !done || res.Text != "echo hi" {
		t.Errorf("Ctrl+Enter = (%+v, %v), want the line run", res, done)
	}
	ed = newKeyEditor(t, Config{}, "echo hi")
	press(t, ed, ParseKey([]byte("\x1b[27;5;27~")))
	if ed.inserting() {
		t.Error("Ctrl+Escape did not leave insert mode like Escape")
	}
}
