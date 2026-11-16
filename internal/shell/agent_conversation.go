package shell

import (
	"strings"

	"github.com/tfcace/hash/internal/editor"
)

type agentConversationMessage struct {
	Role string
	Text string
}

const (
	agentConversationSolidRail         = frameGray + "│" + ansiReset
	agentConversationDashedRail        = frameGray + "┆" + ansiReset
	agentConversationUserLabel         = ansiBold + "you" + ansiReset
	agentConversationReplyPrompt       = agentConversationSolidRail + " " + agentConversationUserLabel + " › "
	agentConversationLiveRailLine      = agentConversationDashedRail
	agentConversationLiveReplyPrompt   = agentConversationDashedRail + " " + agentConversationUserLabel + " › "
	agentConversationReplyPromptWidth  = 8
	agentConversationAgentContinuation = agentConversationSolidRail + "         "
	agentConversationAgentPrefixWidth  = 10 // "│ agent › " and its continuation
	emptyAgentResponseMessage          = "agent ended the turn without text"
	legacyAwaitingInputMarker          = "[AWAITING_INPUT]"
	legacyConversationMarker           = "[CONVERSATION]"
	legacyMarkerTailBytes              = len(legacyAwaitingInputMarker) - 1
)

// agentConversationAgentPrefix opens the agent's first row: its label, in
// the accent color (an escape sequence, "" for none).
func agentConversationAgentPrefix(accent string) string {
	return agentConversationSolidRail + " " + ansiBold + accent + "agent" + ansiReset + " › "
}

// agentConversationInputFrame is the reply box. Opening the rail draws the
// conversation's title row, with the keys as its summary: the frame stays
// open across turns, so it has no last row to put them on.
func agentConversationInputFrame(openRail bool, accent string) *editor.InputFrame {
	frame := &editor.InputFrame{
		Prefix:      agentConversationReplyPrompt,
		LiveTopLine: agentConversationLiveRailLine,
		LivePrefix:  agentConversationLiveReplyPrompt,
		PrefixWidth: agentConversationReplyPromptWidth,
	}
	if openRail {
		const title = "┌ conversation · "
		hints := fitHint("", "", []string{"enter to send", "esc to leave", "/exit to end"}, max(terminalColumns()-len([]rune(title)), 0))
		frame.TopLine = frameTop("conversation", frameGray+hints+ansiReset, accent)
	}
	return frame
}

func agentConversationEditorConfig(base editor.Config, openRail bool, accent string) editor.Config {
	cfg := base
	cfg.Prompt = agentConversationReplyPrompt
	cfg.CompleteOutcomeFunc = nil
	cfg.AgentCompleteLine = nil
	cfg.PrefetchFunc = nil
	cfg.SuggestionFunc = nil
	cfg.Gutter = false
	cfg.InputFrame = agentConversationInputFrame(openRail, accent)
	cfg.DisableHistorySearch = true
	cfg.DisableContextPicker = true
	cfg.DisableLineContinuation = true
	cfg.Incomplete = nil                 // a reply is prose, not a shell line
	cfg.Decline = editor.DeclineEraseRow // Esc leaves, and takes the reply box with it
	return cfg
}

type agentConversationRailPrefixer struct {
	write       func(string)
	first       string // what the agent's first row opens with
	atLineStart bool
	started     bool
}

func newAgentConversationRailPrefixer(write func(string), accent string) *agentConversationRailPrefixer {
	return &agentConversationRailPrefixer{
		write:       write,
		first:       agentConversationAgentPrefix(accent),
		atLineStart: true,
	}
}

func (p *agentConversationRailPrefixer) Write(text string) {
	if text == "" || p.write == nil {
		return
	}

	for text != "" {
		if p.atLineStart {
			if p.started {
				p.write(agentConversationAgentContinuation)
			} else {
				p.write(p.first)
				p.started = true
			}
			p.atLineStart = false
		}

		newline := strings.IndexByte(text, '\n')
		if newline < 0 {
			p.write(text)
			return
		}

		p.write(text[:newline+1])
		text = text[newline+1:]
		p.atLineStart = true
	}
}

type legacyAgentMarkerSanitizer struct {
	pending string
}

func newLegacyAgentMarkerSanitizer() *legacyAgentMarkerSanitizer {
	return &legacyAgentMarkerSanitizer{}
}

func (s *legacyAgentMarkerSanitizer) Write(text string) string {
	if text == "" {
		return ""
	}

	s.pending += text
	s.pending = stripLegacyAgentMarkers(s.pending)

	// Hold back only a tail that could still grow into a marker; the rest
	// shows now. Markers are ASCII and open with "[", so the cut never lands
	// inside a multibyte character.
	emitLen := len(s.pending) - possibleMarkerTail(s.pending)
	out := s.pending[:emitLen]
	s.pending = s.pending[emitLen:]
	return out
}

// possibleMarkerTail is the length of the longest suffix of text that is the
// start of a legacy marker, 0 when none could be.
func possibleMarkerTail(text string) int {
	for n := min(len(text), legacyMarkerTailBytes); n > 0; n-- {
		tail := text[len(text)-n:]
		if strings.HasPrefix(legacyAwaitingInputMarker, tail) || strings.HasPrefix(legacyConversationMarker, tail) {
			return n
		}
	}
	return 0
}

func (s *legacyAgentMarkerSanitizer) Flush() string {
	out := stripLegacyAgentMarkers(s.pending)
	s.pending = ""
	return out
}

func stripLegacyAgentMarkers(text string) string {
	text = strings.ReplaceAll(text, legacyAwaitingInputMarker, "")
	text = strings.ReplaceAll(text, legacyConversationMarker, "")
	return text
}

func agentResponseWantsReply(text string) bool {
	cleaned := strings.TrimSpace(stripLegacyAgentMarkers(text))
	if cleaned == "" {
		return false
	}

	lastLine := lastNonEmptyLine(cleaned)
	if lastLine == "" {
		return false
	}

	trimmed := strings.TrimRight(lastLine, " \t\r\n\"'`*_)]}")
	if strings.HasSuffix(trimmed, "?") || strings.HasSuffix(trimmed, "?!") {
		return true
	}

	lower := strings.ToLower(lastLine)
	followUpPhrases := []string{
		"which would you prefer",
		"would you like",
		"what would you like",
		"which one",
		"should i",
		"shall i",
		"do you want",
		"want me to",
		"tell me",
		"please provide",
		"please choose",
		"choose one",
		"select one",
	}
	for _, phrase := range followUpPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

func agentConversationLooksLikeQuestion(text string) bool {
	line := strings.ToLower(lastNonEmptyLine(stripLegacyAgentMarkers(text)))
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	questionPrefixes := []string{
		"question ",
		"question:",
		"go ahead",
		"please ",
		"tell me",
		"is ",
		"are ",
		"am ",
		"do ",
		"does ",
		"did ",
		"can ",
		"could ",
		"would ",
		"should ",
		"will ",
		"which ",
		"what ",
		"where ",
		"when ",
		"why ",
		"how ",
	}
	for _, prefix := range questionPrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}

	return strings.Contains(line, " question ")
}

func agentConversationReplyEndsConversation(reply string) bool {
	normalized := normalizeAgentConversationControlReply(reply)
	if normalized == "" {
		return false
	}

	switch normalized {
	case "done",
		"exit",
		"quit",
		"cancel",
		"stop",
		"bye",
		"goodbye",
		"q",
		"wq",
		"nevermind",
		"never mind",
		"thats all",
		"that is all",
		"im done",
		"i am done",
		"were done",
		"we are done",
		"ive had enough",
		"i have had enough",
		"had enough",
		"game over",
		"end game",
		"end the game",
		"stop playing",
		"stop the game":
		return true
	default:
		return false
	}
}

func normalizeAgentConversationControlReply(reply string) string {
	normalized := strings.ToLower(strings.TrimSpace(reply))
	normalized = strings.TrimPrefix(normalized, "/")
	normalized = strings.TrimPrefix(normalized, ":")
	replacer := strings.NewReplacer(
		"'", "",
		"’", "",
		",", " ",
		".", " ",
		"!", " ",
		"?", " ",
		";", " ",
		":", " ",
	)
	normalized = replacer.Replace(normalized)
	normalized = strings.Join(strings.Fields(normalized), " ")

	for {
		trimmed := strings.TrimSpace(normalized)
		next := strings.TrimPrefix(trimmed, "ok ")
		next = strings.TrimPrefix(next, "okay ")
		next = strings.TrimPrefix(next, "alright ")
		next = strings.TrimPrefix(next, "all right ")
		next = strings.TrimPrefix(next, "cool ")
		next = strings.TrimPrefix(next, "thanks ")
		next = strings.TrimPrefix(next, "thank you ")
		if next == trimmed {
			return trimmed
		}
		normalized = next
	}
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}
