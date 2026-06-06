package shell

import (
	"strings"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/parser"
)

type agentConversationMessage struct {
	Role string
	Text string
}

const (
	agentConversationReplyPrompt = "you> "
	emptyAgentResponseMessage    = "empty agent response"
	legacyAwaitingInputMarker    = "[AWAITING_INPUT]"
	legacyConversationMarker     = "[CONVERSATION]"
	legacyMarkerTailBytes        = len(legacyAwaitingInputMarker) - 1
)

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
	return agentTurnAllowsReply(commandType, resp) && agentResponseWantsReply(responseText)
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
