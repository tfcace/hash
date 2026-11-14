# Turn-by-Turn Agent Conversation Design

Date: 2026-06-05

## Goal

Hash should let the user continue a `??` agent interaction when the agent asks a question or follow-up. The continuation must not depend on `[AWAITING_INPUT]` or any other user-visible marker.

## Design

Use shell-owned continuation state after full `??` explanatory responses.

- Strip known legacy conversation markers from streamed output and collected response text.
- If the completed response clearly asks for input, enter a reply prompt automatically.
- If the response does not clearly ask for input, keep the normal dismiss/copy flow but add a reply action so the user can continue when the heuristic misses.
- Keep command suggestions on the existing run/edit/cancel path.
- Keep pipe mode single-turn unless explicitly expanded later.

## Conversation Prompt

The prompt is a lightweight shell prompt, not a separate full-screen mode. The user can:

- Type a reply and press Enter to send the next turn.
- Redirect within the conversation, such as "let's pause the game and list my kubernetes contexts".
- Submit an empty line, press Esc, or press Ctrl+C to leave the conversation.

Two ways out are less obvious, and deliberate:

- Typing early leaves the conversation. Keys typed while the agent is still
  writing, its question included, are typeahead for the next prompt, as
  everywhere else in hash: once the agent's answer is over the conversation
  ends and the typed text is at the shell prompt, not in the reply box. Wait
  for the `you>` prompt to reply.
- A follow-up that fails or is canceled (Ctrl+C or Esc while it streams)
  ends the conversation, and the whole `??` line exits 1, even when an
  earlier reply was complete: the line's status says whether the agent's
  last request finished. A line the shell's ending cuts off (a fatal
  signal) ends with 128+signal instead.

## Agent Continuity

ACP transports already keep a protocol session between prompts. Follow-up turns should send a small instruction frame plus the user's latest message through that same session, without duplicating the transcript. The frame tells the agent that side requests are allowed, tool use is appropriate when needed, and the prior conversation state should be preserved.

Stateless transports, such as HTTP, should receive the same instruction frame plus a compact transcript prompt containing prior user and assistant turns plus the new reply.

## Detection

The automatic follow-up heuristic should be conservative:

- Final non-empty line ending with a question mark.
- Common final follow-up phrases such as "which would you prefer", "would you like", "should I", "tell me", or "please provide".

False positives are acceptable only when the user can exit with one key. False negatives are handled by the explicit reply action.

## Tests

Add focused tests for:

- Legacy marker stripping, including split stream chunks.
- Follow-up detection.
- Stateless transcript prompt construction.
- ACP side-request prompt framing without transcript duplication.
- Response confirmation handling with a reply action.
- A multi-turn shell-level flow that sends a follow-up after an assistant question.
- A twenty-questions benchmark where the user pauses the game to ask for Kubernetes contexts, allowing a tool call, then resumes or is asked whether to resume.
