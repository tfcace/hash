// internal/editor/input_test.go
package editor

import (
	"bytes"
	"testing"
)

func TestInputReader_ReadKey_Printable(t *testing.T) {
	input := bytes.NewReader([]byte{'h', 'e', 'l', 'l', 'o'})
	reader := NewInputReader(input)

	key, err := reader.ReadKey()
	if err != nil {
		t.Fatalf("ReadKey() error = %v", err)
	}
	if key.Rune != 'h' {
		t.Errorf("Rune = %q, want 'h'", key.Rune)
	}
}

func TestInputReader_ReadKey_UTF8Hebrew(t *testing.T) {
	input := bytes.NewReader([]byte("ש"))
	reader := NewInputReader(input)

	key, err := reader.ReadKey()
	if err != nil {
		t.Fatalf("ReadKey() error = %v", err)
	}
	if key.Rune != 'ש' {
		t.Fatalf("Rune = %q, want %q", key.Rune, 'ש')
	}
	if key.Special != KeyNone {
		t.Fatalf("Special = %v, want KeyNone", key.Special)
	}
}

func TestInputReader_ReadKey_EscapeSequence(t *testing.T) {
	// Arrow up: ESC [ A
	input := bytes.NewReader([]byte{0x1b, '[', 'A'})
	reader := NewInputReader(input)

	key, err := reader.ReadKey()
	if err != nil {
		t.Fatalf("ReadKey() error = %v", err)
	}
	if key.Special != KeyUp {
		t.Errorf("Special = %v, want KeyUp", key.Special)
	}
}

func TestInputReader_ReadKey_BracketedPaste(t *testing.T) {
	// Bracketed paste: ESC [ 2 0 0 ~ <content> ESC [ 2 0 1 ~
	pasteStart := []byte{0x1b, '[', '2', '0', '0', '~'}
	pasteContent := []byte("hello world")
	pasteEnd := []byte{0x1b, '[', '2', '0', '1', '~'}

	var inputBytes []byte
	inputBytes = append(inputBytes, pasteStart...)
	inputBytes = append(inputBytes, pasteContent...)
	inputBytes = append(inputBytes, pasteEnd...)

	input := bytes.NewReader(inputBytes)
	reader := NewInputReader(input)

	key, err := reader.ReadKey()
	if err != nil {
		t.Fatalf("ReadKey() error = %v", err)
	}
	if key.Special != KeyPaste {
		t.Errorf("Special = %v, want KeyPaste", key.Special)
	}
	if key.PasteText != "hello world" {
		t.Errorf("PasteText = %q, want %q", key.PasteText, "hello world")
	}
}

func TestInputReader_ReadKey_BracketedPaste_Multiline(t *testing.T) {
	// Bracketed paste with multiline content
	pasteStart := []byte{0x1b, '[', '2', '0', '0', '~'}
	pasteContent := []byte("line1\nline2\nline3")
	pasteEnd := []byte{0x1b, '[', '2', '0', '1', '~'}

	var inputBytes []byte
	inputBytes = append(inputBytes, pasteStart...)
	inputBytes = append(inputBytes, pasteContent...)
	inputBytes = append(inputBytes, pasteEnd...)

	input := bytes.NewReader(inputBytes)
	reader := NewInputReader(input)

	key, err := reader.ReadKey()
	if err != nil {
		t.Fatalf("ReadKey() error = %v", err)
	}
	if key.Special != KeyPaste {
		t.Errorf("Special = %v, want KeyPaste", key.Special)
	}
	if key.PasteText != "line1\nline2\nline3" {
		t.Errorf("PasteText = %q, want %q", key.PasteText, "line1\nline2\nline3")
	}
}

func TestIsPasteStart(t *testing.T) {
	tests := []struct {
		input []byte
		want  bool
	}{
		{[]byte{0x1b, '[', '2', '0', '0', '~'}, true},
		{[]byte{0x1b, '[', '2', '0', '1', '~'}, false},
		{[]byte{0x1b, '[', 'A'}, false},
		{[]byte{0x1b}, false},
	}
	for _, tt := range tests {
		got := isPasteStart(tt.input)
		if got != tt.want {
			t.Errorf("isPasteStart(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsPasteEnd(t *testing.T) {
	tests := []struct {
		input []byte
		want  bool
	}{
		{[]byte{0x1b, '[', '2', '0', '1', '~'}, true},
		{[]byte("hello" + string([]byte{0x1b, '[', '2', '0', '1', '~'})), true},
		{[]byte{0x1b, '[', '2', '0', '0', '~'}, false},
		{[]byte{0x1b, '[', 'A'}, false},
		{[]byte{0x1b}, false},
	}
	for _, tt := range tests {
		got := isPasteEnd(tt.input)
		if got != tt.want {
			t.Errorf("isPasteEnd(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestInputReader_ReadKey_BracketedPaste_LargeContent(t *testing.T) {
	// Test that large paste content is truncated to maxPasteSize
	// Use a smaller test size to avoid allocating 10MB in tests
	const testMaxSize uint = 1024

	pasteStart := []byte{0x1b, '[', '2', '0', '0', '~'}
	largeContent := bytes.Repeat([]byte("x"), int(testMaxSize)+100)
	pasteEnd := []byte{0x1b, '[', '2', '0', '1', '~'}

	var inputBytes []byte
	inputBytes = append(inputBytes, pasteStart...)
	inputBytes = append(inputBytes, largeContent...)
	inputBytes = append(inputBytes, pasteEnd...)

	input := bytes.NewReader(inputBytes)
	reader := NewInputReader(input)
	reader.SetMaxPasteSize(testMaxSize)

	key, err := reader.ReadKey()
	if err != nil {
		t.Fatalf("ReadKey() error = %v", err)
	}
	if key.Special != KeyPaste {
		t.Errorf("Special = %v, want KeyPaste", key.Special)
	}
	// Content should be truncated to testMaxSize
	if uint(len(key.PasteText)) > testMaxSize {
		t.Errorf("PasteText length = %d, want <= %d", len(key.PasteText), testMaxSize)
	}
}

func TestInputReader_ReadKey_SS3Arrow(t *testing.T) {
	// Application cursor keys mode: right arrow arrives as ESC O C.
	input := bytes.NewReader([]byte{0x1b, 'O', 'C'})
	reader := NewInputReader(input)

	key, err := reader.ReadKey()
	if err != nil {
		t.Fatalf("ReadKey() error = %v", err)
	}
	if key.Special != KeyRight {
		t.Errorf("Special = %v, want KeyRight", key.Special)
	}
}

func TestInputReader_ReadKey_AltO_StillWorks(t *testing.T) {
	// A bare ESC O (nothing following) is Alt+O, not a truncated SS3.
	input := bytes.NewReader([]byte{0x1b, 'O'})
	reader := NewInputReader(input)

	key, err := reader.ReadKey()
	if err != nil {
		t.Fatalf("ReadKey() error = %v", err)
	}
	if key.Rune != 'O' || !key.Alt {
		t.Errorf("got Special=%v Rune=%q Alt=%v, want Alt+O", key.Special, key.Rune, key.Alt)
	}
}
