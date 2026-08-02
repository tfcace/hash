// internal/editor/ghost.go
package editor

// GhostText represents inline suggestion text that appears after the cursor.
// Ghost text is shown in dim gray and can be accepted with Tab or dismissed with Esc.
type GhostText struct {
	Text       string // The full ghost text suggestion
	AcceptedAt int    // Number of characters already accepted (for partial acceptance)
	Active     bool   // Whether ghost text is currently displayed
	Streaming  bool   // Whether more text is still arriving
	FromAgent  bool   // True for agent suggestions (show hints), false for predictions (fish-style)
	Status     string // Transient activity; never accepted as command text.
}

// GhostStreamUpdate keeps streamed ghost text and its transient agent state
// together so the editor owns one coherent render state.
type GhostStreamUpdate struct {
	Text   string
	Status string
}

// NewGhostText creates a new ghost text state.
func NewGhostText() *GhostText {
	return &GhostText{}
}

// Set sets the ghost text content.
func (g *GhostText) Set(text string) {
	g.Text = text
	g.AcceptedAt = 0
	g.Active = true
}

// Append adds more text to the ghost (for streaming).
func (g *GhostText) Append(text string) {
	g.Text += text
	g.Active = true
}

// Clear removes the ghost text.
func (g *GhostText) Clear() {
	g.Text = ""
	g.AcceptedAt = 0
	g.Active = false
	g.Streaming = false
	g.FromAgent = false
	g.Status = ""
}

// Remaining returns the unaccepted portion of ghost text.
func (g *GhostText) Remaining() string {
	if g.AcceptedAt >= len(g.Text) {
		return ""
	}
	return g.Text[g.AcceptedAt:]
}

// AcceptAll accepts all remaining ghost text.
// Returns the text that should be inserted.
func (g *GhostText) AcceptAll() string {
	text := g.Remaining()
	g.Clear()
	return text
}

// AcceptWord accepts the next word of ghost text.
// Returns the text that should be inserted.
func (g *GhostText) AcceptWord() string {
	remaining := g.Remaining()
	if remaining == "" {
		g.Clear()
		return ""
	}

	// Find end of next word (including trailing space)
	end := 0
	inWord := false
	for i, r := range remaining {
		if r == ' ' || r == '\t' {
			if inWord {
				// Found end of word, include trailing spaces
				for j := i; j < len(remaining); j++ {
					if remaining[j] != ' ' && remaining[j] != '\t' {
						end = j
						break
					}
					end = j + 1
				}
				break
			}
		} else {
			inWord = true
		}
		end = i + 1
	}

	accepted := remaining[:end]
	g.AcceptedAt += end

	// If we've accepted everything, clear
	if g.AcceptedAt >= len(g.Text) {
		g.Clear()
	}

	return accepted
}

// AcceptChar accepts the next character of ghost text.
// Returns the character that should be inserted.
func (g *GhostText) AcceptChar() string {
	remaining := g.Remaining()
	if remaining == "" {
		g.Clear()
		return ""
	}

	// Get first rune
	runes := []rune(remaining)
	r := runes[0]
	g.AcceptedAt += len(string(r))
	if g.AcceptedAt >= len(g.Text) {
		g.Clear()
	}
	return string(r)
}

// IsEmpty returns true if there's no ghost text.
func (g *GhostText) IsEmpty() bool {
	return g.Remaining() == ""
}

// SetStreaming marks the ghost text as still receiving data.
func (g *GhostText) SetStreaming(streaming bool) {
	g.Streaming = streaming
	if streaming {
		g.Active = true
	}
}
