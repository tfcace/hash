# Turn-by-Turn Agent Conversation Manual Testing Checklist

This checklist covers marker-free follow-up conversations for full `??` agent requests.

## Prerequisites

- Build the hash binary: `go build -o hash ./cmd/hash`
- Configure an agent in `~/.config/hash/config.toml`
- Use a working agent such as Claude Agent ACP, Gemini CLI ACP, or a configured HTTP/local model

## Test Cases

### 1. Basic Single-Turn Explanation

**Test:** Verify normal explanatory responses can be dismissed.

**Steps:**
1. Run `hash`
2. Enter: `?? explain what pwd does`
3. Wait for the response
4. Press Enter

**Expected:**
- Response streams without hidden markers
- Hints include done, copy, reply, and cancel actions
- Enter dismisses the response and returns to the normal shell prompt

### 2. Automatic Follow-Up Prompt

**Test:** Verify Hash prompts for input when the agent ends with a question.

**Steps:**
1. Run `hash`
2. Enter a prompt likely to need clarification, such as `?? help me find a config file but ask me which directory first`
3. Wait for the agent to ask a question

**Expected:**
- No `[AWAITING_INPUT]` marker is displayed
- Hash shows a `you> ` reply prompt
- The normal shell prompt does not appear before the reply prompt

### 3. Reply Flow

**Test:** Verify a reply is sent as the next agent turn.

**Steps:**
1. Enter automatic follow-up mode from test 2
2. Type a reply, such as `internal/shell`
3. Press Enter

**Expected:**
- Reply is sent to the agent
- Agent response streams normally
- If the agent asks another question, another `you> ` prompt appears
- If the agent finishes, reply-capable confirmation hints appear

### 4. Explicit Reply Action

**Test:** Verify the user can continue even when Hash does not auto-detect a question.

**Steps:**
1. Run `hash`
2. Enter: `?? explain this repository structure briefly`
3. When the response finishes, press `r`
4. Type a follow-up question and press Enter

**Expected:**
- Pressing `r` opens the `you> ` prompt
- The follow-up is sent to the same conversation flow
- Pressing Enter instead of `r` dismisses the response

### 5. Legacy Marker Compatibility

**Test:** Verify legacy markers are stripped if an agent still emits them.

**Steps:**
1. Use or mock an agent response ending in `[AWAITING_INPUT]`
2. Run a full `??` request

**Expected:**
- `[AWAITING_INPUT]` is not visible
- `[CONVERSATION]` is not visible if emitted
- If the visible final response asks a question, `you> ` appears

### 6. Command Suggestion Unchanged

**Test:** Verify command suggestions keep the existing run/edit/cancel flow.

**Steps:**
1. Run `hash`
2. Enter: `?? command to list hidden files`
3. Wait for a command suggestion

**Expected:**
- Existing command confirmation hints appear
- Enter runs, Tab edits, Esc cancels
- No `you> ` prompt appears for command suggestions

### 7. Pipe Mode Unchanged

**Test:** Verify pipe mode remains single-turn.

**Steps:**
1. Run: `ls | ?? summarize these files`

**Expected:**
- Agent receives pipe output as context
- Response streams normally
- No automatic `you> ` prompt appears

### 8. Exit Reply Prompt

**Test:** Verify the reply prompt exits cleanly.

**Steps:**
1. Enter automatic follow-up mode
2. Press Enter on an empty `you> ` prompt
3. Repeat and press Ctrl+C at the `you> ` prompt

**Expected:**
- Empty input exits the conversation
- Ctrl+C exits the conversation
- Normal shell prompt returns

### 9. Twenty Questions With Side Request

**Test:** Verify the user can pause an ongoing conversation for tool-backed work.

**Steps:**
1. Run `hash`
2. Enter: `?? Let's play twenty questions. You guess what repo file I'm thinking of. Ask one yes/no question at a time.`
3. Answer one or two questions at `you> `
4. Type: `let's pause the game and list my kubernetes contexts`
5. Approve or deny any resulting tool permission prompt

**Expected:**
- The side request is sent as the next agent turn
- The agent can request an appropriate tool call
- Permission prompt rendering does not break streaming or the reply prompt
- The agent preserves the twenty-questions state and resumes or asks whether to resume
- No marker text is displayed

## Notes

Turn-by-turn continuation is controlled by Hash, not by agent-visible markers. ACP agents reuse their protocol session for follow-up turns. Stateless transports receive a compact transcript prompt.
