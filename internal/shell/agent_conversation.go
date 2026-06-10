package shell

import (
	"strings"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/parser"
)

type agentConversationMessage struct {
	Role string
	Text string
}

const (
	agentConversationLiveRailStyle     = "\x1b[1;38;2;255;209;102m"
	agentConversationLabelStyle        = "\x1b[1;38;2;94;234;212m"
	agentConversationSolidRail         = agentConversationLiveRailStyle + "│\x1b[0m"
	agentConversationDashedRail        = agentConversationLiveRailStyle + "┆\x1b[0m"
	agentConversationUserLabel         = agentConversationLabelStyle + "you\x1b[0m"
	agentConversationAgentLabel        = agentConversationLabelStyle + "agent\x1b[0m"
	agentConversationReplyPrompt       = agentConversationSolidRail + " " + agentConversationUserLabel + " › "
	agentConversationLiveRailLine      = agentConversationDashedRail
	agentConversationLiveReplyPrompt   = agentConversationDashedRail + " " + agentConversationUserLabel + " › "
	agentConversationReplyPromptWidth  = 8
	agentConversationAgentPrefix       = agentConversationSolidRail + " " + agentConversationAgentLabel + " › "
	agentConversationAgentContinuation = agentConversationSolidRail + "         "
	emptyAgentResponseMessage          = "agent ended the turn without text"
	legacyAwaitingInputMarker          = "[AWAITING_INPUT]"
	legacyConversationMarker           = "[CONVERSATION]"
	legacyMarkerTailBytes              = len(legacyAwaitingInputMarker) - 1
)

func agentConversationInputFrame(openRail bool) *editor.InputFrame {
	frame := &editor.InputFrame{
		Prefix:      agentConversationReplyPrompt,
		LiveTopLine: agentConversationLiveRailLine,
		LivePrefix:  agentConversationLiveReplyPrompt,
		PrefixWidth: agentConversationReplyPromptWidth,
	}
	if openRail {
		frame.TopLine = agentConversationLiveRailStyle + "╭─ conversation\x1b[0m \x1b[90mEnter sends · Esc/Ctrl+C leaves · /exit ends\x1b[0m"
	}
	return frame
}

func agentConversationEditorConfig(base editor.Config, openRail bool) editor.Config {
	cfg := base
	cfg.Prompt = agentConversationReplyPrompt
	cfg.CompleteFunc = nil
	cfg.PrefetchFunc = nil
	cfg.SuggestionFunc = nil
	cfg.Gutter = false
	cfg.InputFrame = agentConversationInputFrame(openRail)
	cfg.DisableHistorySearch = true
	cfg.DisableContextPicker = true
	cfg.DisableLineContinuation = true
	cfg.CancelOnEscape = true
	return cfg
}

type agentConversationRailPrefixer struct {
	write       func(string)
	atLineStart bool
	started     bool
}

func newAgentConversationRailPrefixer(write func(string)) *agentConversationRailPrefixer {
	return &agentConversationRailPrefixer{
		write:       write,
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
				p.write(agentConversationAgentPrefix)
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
	if len(s.pending) <= legacyMarkerTailBytes {
		return ""
	}

	emitLen := len(s.pending) - legacyMarkerTailBytes
	out := s.pending[:emitLen]
	s.pending = s.pending[emitLen:]
	return out
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

func agentTurnAllowsReply(commandType parser.CommandType, resp agent.Response) bool {
	return commandType == parser.CommandTypeAgent && resp.Type == agent.ResponseTypeExplanation
}

func agentTurnShouldPromptForReply(commandType parser.CommandType, resp agent.Response, responseText string) bool {
	if commandType != parser.CommandTypeAgent || !agentResponseWantsReply(responseText) {
		return false
	}
	if resp.Type == agent.ResponseTypeExplanation {
		return true
	}
	return resp.Type == agent.ResponseTypeCommand && agentConversationLooksLikeQuestion(responseText)
}

func agentTurnResponseForConfirmation(commandType parser.CommandType, resp agent.Response, responseText string) agent.Response {
	if commandType == parser.CommandTypeAgent &&
		resp.Type == agent.ResponseTypeCommand &&
		agentResponseWantsReply(responseText) &&
		agentConversationLooksLikeQuestion(responseText) {
		return agent.Response{
			Type:        agent.ResponseTypeExplanation,
			Explanation: responseText,
		}
	}
	return resp
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
