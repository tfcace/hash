package markdown

import "testing"

// Closing inline code inside bold must leave the bold on: one SGR reset ends
// both styles, so the still-open one has to be re-emitted.
func TestStreaming_BoldSurvivesNestedCodeReset(t *testing.T) {
	r := NewStreamingRenderer()
	got := r.Write("**bold `code` still bold**\n")
	want := bold + "bold " + cyan + "code" + reset + bold + " still bold" + reset + "\n"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// Inside a code span, asterisks are literal: they neither toggle bold nor
// end the code color.
func TestStreaming_StarsAreLiteralInsideCode(t *testing.T) {
	r := NewStreamingRenderer()
	got := r.Write("`a **b** c`\n")
	want := cyan + "a **b** c" + reset + "\n"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// Finish ends the stream: the next stream starts outside any code block.
func TestStreaming_FinishResetsCodeBlock(t *testing.T) {
	r := NewStreamingRenderer()
	r.Write("```sh\nls\n")
	r.Finish()
	if got := r.Write("plain\n"); got != "plain\n" {
		t.Fatalf("after Finish got %q, want plain text", got)
	}
}
