package completion

import "testing"

func TestShellWordAt(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "plain word",
			line: "cp file",
			want: "file",
		},
		{
			name: "escaped space",
			line: `cp My\ File`,
			want: `My\ File`,
		},
		{
			name: "single quoted space",
			line: `cp 'My File`,
			want: `'My File`,
		},
		{
			name: "double quoted space",
			line: `cp "My File`,
			want: `"My File`,
		},
		{
			name: "space after completed escaped word",
			line: `cp My\ File `,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellWordAt(tt.line, len(tt.line)); got != tt.want {
				t.Fatalf("shellWordAt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShellUnescapeWord(t *testing.T) {
	tests := []struct {
		name string
		word string
		want string
	}{
		{name: "escaped space", word: `My\ File`, want: "My File"},
		{name: "single quote", word: `'My File`, want: "My File"},
		{name: "double quote", word: `"My File`, want: "My File"},
		{name: "escaped single quote", word: `quote\'s.txt`, want: "quote's.txt"},
		{name: "escaped double quote", word: `double\"quote.txt`, want: `double"quote.txt`},
		{name: "escaped backslash", word: `back\\slash.txt`, want: `back\slash.txt`},
		{name: "double quoted literal backslash", word: `"back\slash`, want: `back\slash`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellUnescapeWord(tt.word); got != tt.want {
				t.Fatalf("shellUnescapeWord() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEscapeShellWord(t *testing.T) {
	tests := []struct {
		name string
		word string
		want string
	}{
		{name: "safe", word: "src/main.go", want: "src/main.go"},
		{name: "space", word: "My File.txt", want: `My\ File.txt`},
		{name: "single quote", word: "quote's.txt", want: `quote\'s.txt`},
		{name: "double quote", word: `double"quote.txt`, want: `double\"quote.txt`},
		{name: "backslash", word: `back\slash.txt`, want: `back\\slash.txt`},
		{name: "semicolon", word: "semi;colon.txt", want: `semi\;colon.txt`},
		{name: "dollar", word: "price$1.txt", want: `price\$1.txt`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EscapeShellWord(tt.word); got != tt.want {
				t.Fatalf("EscapeShellWord() = %q, want %q", got, tt.want)
			}
		})
	}
}
