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
