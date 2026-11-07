// internal/editor/ghost.go
package editor

import "strings"

// ghostState is the lifecycle of the inline suggestion after the cursor.
type ghostState int

const (
	// ghostNone: nothing shown after the cursor.
	ghostNone ghostState = iota
	// ghostPrediction: a fish-style history or learned-fix suggestion. Right
	// accepts it; Tab drops it and completes, Enter drops it and runs the
	// line as typed.
	ghostPrediction
	// ghostAgentStreaming: an agent fill is arriving on the attached stream.
	// Before the first chunk the Status (or "thinking") shows instead.
	ghostAgentStreaming
	// ghostAgentDone: the stream ended with a fill; Tab or Enter accept it.
	ghostAgentDone
	// ghostAgentFailed: the stream ended with an error. Notice says why until
	// the next key.
	ghostAgentFailed
)

// GhostText represents inline suggestion text that appears after the cursor.
// Ghost text is shown in dim gray and can be accepted with Tab or dismissed
// with Esc.
//
// Invariants: the stream channels and cancel are attached only in
// ghostAgentStreaming, where a nil channel is one that has closed; the
// stream ends once both are nil. ghostPrediction and ghostAgentDone always
// have unaccepted text; status belongs to ghostAgentStreaming and notice to
// ghostAgentFailed. Only the methods below change any of it.
type GhostText struct {
	text     string // the full suggestion
	accepted int    // bytes of text already taken into the buffer
	status   string // transient agent activity; never accepted as command text
	notice   string // why the agent fill failed

	state   ghostState
	updates <-chan GhostStreamUpdate
	errs    <-chan error
	cancel  func() // stops the request behind the stream; nil when there is none
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

// Set shows text as a prediction. Empty text clears the ghost.
func (g *GhostText) Set(text string) {
	g.Clear()
	if text != "" {
		g.text = text
		g.state = ghostPrediction
	}
}

// startStream attaches an agent stream; its chunks fill the ghost.
func (g *GhostText) startStream(updates <-chan GhostStreamUpdate, errs <-chan error, cancel func()) {
	g.Clear()
	g.state = ghostAgentStreaming
	g.updates = updates
	g.errs = errs
	g.cancel = cancel
}

// update applies a streamed chunk. It is a no-op once the stream is detached.
func (g *GhostText) update(u GhostStreamUpdate) {
	if g.state != ghostAgentStreaming {
		return
	}
	g.status = u.Status
	g.text += u.Text
}

// updatesClosed is the text channel closing. The producer may have sent an
// error just before, which the editor's select can take after the close, so
// the stream stays open until the error channel closes too.
func (g *GhostText) updatesClosed() {
	if g.state != ghostAgentStreaming {
		return
	}
	g.updates = nil
	if g.errs == nil {
		g.endStream()
	}
}

// errsClosed is the error channel closing without an error.
func (g *GhostText) errsClosed() {
	if g.state != ghostAgentStreaming {
		return
	}
	g.errs = nil
	if g.updates == nil {
		g.endStream()
	}
}

// endStream is the stream ending without an error: the fill is complete, or
// there was none.
func (g *GhostText) endStream() {
	if g.state != ghostAgentStreaming {
		return
	}
	text, accepted := g.text, g.accepted
	g.Clear()
	if accepted < len(text) {
		g.text, g.accepted = text, accepted
		g.state = ghostAgentDone
	}
}

// fail ends the stream with an error. An empty notice (the user canceled)
// leaves nothing behind.
func (g *GhostText) fail(notice string) {
	g.Clear()
	if notice != "" {
		g.notice = notice
		g.state = ghostAgentFailed
	}
}

// Clear removes the ghost and detaches any stream, canceling its request:
// nothing will read the rest. (After the stream ended on its own, the
// cancel only releases the finished request.)
func (g *GhostText) Clear() {
	if g.state == ghostAgentStreaming && g.cancel != nil {
		g.cancel()
	}
	*g = GhostText{}
}

// visible reports whether the ghost occupies the space after the cursor and
// so gets the first look at keys.
func (g *GhostText) visible() bool {
	return g.state == ghostPrediction || g.state == ghostAgentStreaming || g.state == ghostAgentDone
}

// fromAgent reports whether the ghost is an agent fill (with accept hints)
// rather than a fish-style prediction.
func (g *GhostText) fromAgent() bool {
	return g.state == ghostAgentStreaming || g.state == ghostAgentDone
}

// streaming reports whether the agent fill is still arriving.
func (g *GhostText) streaming() bool {
	return g.state == ghostAgentStreaming
}

// acceptedAny reports whether part of the fill is already in the buffer.
func (g *GhostText) acceptedAny() bool {
	return g.accepted > 0
}

// shownWhole reports whether the unaccepted text is drawn whole after the
// cursor, so accepting it puts nothing on the line the user has not seen.
func (g *GhostText) shownWhole() bool {
	_, hidden := ghostShown(g.Remaining())
	return hidden == 0
}

// ghostShown is how ghost text is drawn after the cursor: its first line,
// and how many lines after it are not drawn (blank trailing lines aside).
func ghostShown(text string) (first string, hidden int) {
	first, rest, _ := strings.Cut(text, "\n")
	if rest = strings.TrimRight(rest, " \t\n"); rest == "" {
		return first, 0
	}
	return first, strings.Count(rest, "\n") + 1
}

// Remaining returns the unaccepted portion of ghost text.
func (g *GhostText) Remaining() string {
	if g.accepted >= len(g.text) {
		return ""
	}
	return g.text[g.accepted:]
}

// AcceptAll accepts all remaining ghost text; a fill still streaming stays
// attached for the text that follows.
// Returns the text that should be inserted.
func (g *GhostText) AcceptAll() string {
	text := g.Remaining()
	g.accept(len(text))
	return text
}

// accept marks n more bytes accepted. A finished ghost with nothing left
// clears; a streaming one stays attached, since more text may follow.
func (g *GhostText) accept(n int) {
	g.accepted += n
	if g.accepted >= len(g.text) && g.state != ghostAgentStreaming {
		g.Clear()
	}
}

// AcceptWord accepts the next word of ghost text with the blanks around
// it; a newline is a blank, so a word never runs into the next line. It
// scans bytes: blanks are ASCII, so a multibyte rune (or a stray invalid
// byte) is never split or overrun.
// Returns the text that should be inserted.
func (g *GhostText) AcceptWord() string {
	remaining := g.Remaining()
	blank := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' }
	end := 0
	for end < len(remaining) && blank(remaining[end]) {
		end++
	}
	for end < len(remaining) && !blank(remaining[end]) {
		end++
	}
	for end < len(remaining) && blank(remaining[end]) {
		end++
	}

	accepted := remaining[:end]
	g.accept(end)
	return accepted
}
