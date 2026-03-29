// internal/editor/key_test.go
package editor

import "testing"

func TestParseKey_Printable(t *testing.T) {
	key := ParseKey([]byte{'a'})
	if key.Rune != 'a' {
		t.Errorf("Rune = %q, want 'a'", key.Rune)
	}
	if key.Special != KeyNone {
		t.Errorf("Special = %v, want KeyNone", key.Special)
	}
}

func TestParseKey_Enter(t *testing.T) {
	key := ParseKey([]byte{'\r'})
	if key.Special != KeyEnter {
		t.Errorf("Special = %v, want KeyEnter", key.Special)
	}
}

func TestParseKey_Escape(t *testing.T) {
	key := ParseKey([]byte{0x1b})
	if key.Special != KeyEscape {
		t.Errorf("Special = %v, want KeyEscape", key.Special)
	}
}

func TestParseKey_ArrowUp(t *testing.T) {
	key := ParseKey([]byte{0x1b, '[', 'A'})
	if key.Special != KeyUp {
		t.Errorf("Special = %v, want KeyUp", key.Special)
	}
}

func TestParseKey_ArrowDown(t *testing.T) {
	key := ParseKey([]byte{0x1b, '[', 'B'})
	if key.Special != KeyDown {
		t.Errorf("Special = %v, want KeyDown", key.Special)
	}
}

func TestParseKey_CtrlA(t *testing.T) {
	key := ParseKey([]byte{0x01})
	if !key.Ctrl || key.Rune != 'a' {
		t.Errorf("Expected Ctrl+a, got Ctrl=%v Rune=%q", key.Ctrl, key.Rune)
	}
}

func TestParseKey_AltLeft(t *testing.T) {
	// Alt+Left: ESC [ 1 ; 3 D
	key := ParseKey([]byte{0x1b, '[', '1', ';', '3', 'D'})
	if !key.Alt || key.Special != KeyLeft {
		t.Errorf("Expected Alt+Left, got Alt=%v Special=%v", key.Alt, key.Special)
	}
}

func TestParseKey_Backspace(t *testing.T) {
	key := ParseKey([]byte{0x7f})
	if key.Special != KeyBackspace {
		t.Errorf("Special = %v, want KeyBackspace", key.Special)
	}
}

func TestParseKey_ShiftEnter_CSIu(t *testing.T) {
	// Shift+Enter in CSI u encoding: ESC [ 13 ; 2 u
	key := ParseKey([]byte{0x1b, '[', '1', '3', ';', '2', 'u'})
	if key.Special != KeyEnter {
		t.Errorf("Special = %v, want KeyEnter", key.Special)
	}
	if !key.Shift {
		t.Error("Shift = false, want true")
	}
}

func TestParseKey_CtrlEnter_CSIu(t *testing.T) {
	// Ctrl+Enter in CSI u encoding: ESC [ 13 ; 5 u
	key := ParseKey([]byte{0x1b, '[', '1', '3', ';', '5', 'u'})
	if key.Special != KeyEnter {
		t.Errorf("Special = %v, want KeyEnter", key.Special)
	}
	if !key.Ctrl {
		t.Error("Ctrl = false, want true")
	}
}

func TestParseKey_PlainEnter_CSIu(t *testing.T) {
	// Plain Enter in CSI u encoding: ESC [ 13 u (no modifier)
	key := ParseKey([]byte{0x1b, '[', '1', '3', 'u'})
	if key.Special != KeyEnter {
		t.Errorf("Special = %v, want KeyEnter", key.Special)
	}
	if key.Shift || key.Alt || key.Ctrl {
		t.Errorf("Expected no modifiers, got Shift=%v Alt=%v Ctrl=%v", key.Shift, key.Alt, key.Ctrl)
	}
}

func TestParseKey_PlainTab_CSIu(t *testing.T) {
	// Plain Tab in CSI u encoding: ESC [ 9 u (no modifier)
	key := ParseKey([]byte{0x1b, '[', '9', 'u'})
	if key.Special != KeyTab {
		t.Errorf("Special = %v, want KeyTab", key.Special)
	}
	if key.Shift || key.Alt || key.Ctrl {
		t.Errorf("Expected no modifiers, got Shift=%v Alt=%v Ctrl=%v", key.Shift, key.Alt, key.Ctrl)
	}
}

func TestParseKey_ShiftTab_CSIu(t *testing.T) {
	// Shift+Tab in CSI u encoding: ESC [ 9 ; 2 u
	key := ParseKey([]byte{0x1b, '[', '9', ';', '2', 'u'})
	if key.Special != KeyTab {
		t.Errorf("Special = %v, want KeyTab", key.Special)
	}
	if !key.Shift {
		t.Error("Shift = false, want true")
	}
}

func TestParseKey_CtrlTab_CSIu(t *testing.T) {
	// Ctrl+Tab in CSI u encoding: ESC [ 9 ; 5 u
	key := ParseKey([]byte{0x1b, '[', '9', ';', '5', 'u'})
	if key.Special != KeyTab {
		t.Errorf("Special = %v, want KeyTab", key.Special)
	}
	if !key.Ctrl {
		t.Error("Ctrl = false, want true")
	}
}

func TestParseKey_DECRPM_Discarded(t *testing.T) {
	// DECRPM response: ESC [ ? 2027 ; 1 $ y — should be silently discarded
	key := ParseKey([]byte{0x1b, '[', '?', '2', '0', '2', '7', ';', '1', '$', 'y'})
	if key.Special != KeyNone || key.Rune != 0 {
		t.Errorf("DECRPM should be discarded (zero Key), got Special=%v Rune=%q", key.Special, key.Rune)
	}
}

func TestParseKey_DECRPM_Mode2026_Discarded(t *testing.T) {
	// DECRPM response for mode 2026: ESC [ ? 2026 ; 2 $ y
	key := ParseKey([]byte{0x1b, '[', '?', '2', '0', '2', '6', ';', '2', '$', 'y'})
	if key.Special != KeyNone || key.Rune != 0 {
		t.Errorf("DECRPM should be discarded, got Special=%v Rune=%q", key.Special, key.Rune)
	}
}

func TestParseKey_DA_Discarded(t *testing.T) {
	// Device Attributes response: ESC [ ? 62 ; 4 c
	key := ParseKey([]byte{0x1b, '[', '?', '6', '2', ';', '4', 'c'})
	if key.Special != KeyNone || key.Rune != 0 {
		t.Errorf("DA response should be discarded, got Special=%v Rune=%q", key.Special, key.Rune)
	}
}

// TestParseKey_AllCSIQuestionMark_Discarded verifies that all CSI ? sequences
// (terminal responses like DECRPM, DA, XTVERSION, etc.) are silently discarded.
// This is a regression test for ghosting artifacts in Ctrl+R search.
func TestParseKey_AllCSIQuestionMark_Discarded(t *testing.T) {
	tests := []struct {
		name string
		seq  []byte
	}{
		{"DECRPM mode 2027 set", []byte{0x1b, '[', '?', '2', '0', '2', '7', ';', '1', '$', 'y'}},
		{"DECRPM mode 2027 reset", []byte{0x1b, '[', '?', '2', '0', '2', '7', ';', '2', '$', 'y'}},
		{"DECRPM mode 2026 set", []byte{0x1b, '[', '?', '2', '0', '2', '6', ';', '1', '$', 'y'}},
		{"DECRPM mode 2026 reset", []byte{0x1b, '[', '?', '2', '0', '2', '6', ';', '2', '$', 'y'}},
		{"DECRPM mode 1 set", []byte{0x1b, '[', '?', '1', ';', '1', '$', 'y'}},
		{"DA primary", []byte{0x1b, '[', '?', '6', '2', ';', '4', 'c'}},
		{"DA secondary", []byte{0x1b, '[', '?', '1', ';', '0', 'c'}},
		{"XTVERSION-like", []byte{0x1b, '[', '?', '6', '5', 'n'}},
		{"minimal CSI ?", []byte{0x1b, '[', '?', 'x'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := ParseKey(tt.seq)
			if key != (Key{}) {
				t.Errorf("CSI ? sequence %q should be discarded (zero Key), got %+v", tt.seq, key)
			}
		})
	}
}

// TestParseKey_CSIQuestionMark_DoesNotAffectNormalKeys verifies that the
// DECRPM discard logic doesn't accidentally eat normal CSI sequences.
func TestParseKey_CSIQuestionMark_DoesNotAffectNormalKeys(t *testing.T) {
	normalTests := []struct {
		name    string
		seq     []byte
		special KeyCode
	}{
		{"arrow up", []byte{0x1b, '[', 'A'}, KeyUp},
		{"arrow down", []byte{0x1b, '[', 'B'}, KeyDown},
		{"arrow right", []byte{0x1b, '[', 'C'}, KeyRight},
		{"arrow left", []byte{0x1b, '[', 'D'}, KeyLeft},
		{"home", []byte{0x1b, '[', 'H'}, KeyHome},
		{"end", []byte{0x1b, '[', 'F'}, KeyEnd},
		{"delete", []byte{0x1b, '[', '3', '~'}, KeyDelete},
	}

	for _, tt := range normalTests {
		t.Run(tt.name, func(t *testing.T) {
			key := ParseKey(tt.seq)
			if key.Special != tt.special {
				t.Errorf("normal CSI %q: got Special=%v, want %v", tt.seq, key.Special, tt.special)
			}
		})
	}
}
