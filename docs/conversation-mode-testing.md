# Conversation Mode Manual Testing Checklist

This document describes the manual testing steps for the conversation mode feature.

## Prerequisites

- Build the hash binary: `go build -o hash ./cmd/hash`
- Configure an agent in `~/.config/hash/config.toml` that supports the `[AWAITING_INPUT]` marker
- Have a working agent available (Claude Code, Gemini CLI, or local model)

## Test Cases

### 1. Basic Conversation Flow (No Marker)

**Test:** Verify single-turn UI still works for responses without marker.

**Steps:**
1. Run `hash`
2. Enter: `?? what time is it?`
3. Wait for agent response

**Expected:**
- Agent responds with current time information
- Single-turn confirmation UI appears (Run/Edit/Cancel)
- No conversation mode activated
- Shell returns to normal prompt after confirmation

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 2. Conversation Mode Trigger

**Test:** Verify conversation mode activates when agent sends `[AWAITING_INPUT]` marker.

**Steps:**
1. Run `hash`
2. Use a prompt that triggers the agent to ask a follow-up question
3. Agent should respond with `[AWAITING_INPUT]` at the end

**Expected:**
- Agent response is displayed (without the `[AWAITING_INPUT]` marker visible)
- Background tint appears (subtle color based on prompt theme)
- `║` input prompt appears in accent color
- Hints appear: "Esc exit · !cmd shell"
- Cursor is ready for user input

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 3. Reply Flow

**Test:** Verify multi-turn conversation works.

**Steps:**
1. After entering conversation mode (see test 2)
2. Type a reply to the agent's question
3. Press Enter
4. Wait for agent response

**Expected:**
- Input is sent to agent
- "Thinking" indicator appears briefly
- Agent response streams in with markdown rendering
- If agent includes `[AWAITING_INPUT]` again, conversation continues
- If no marker, conversation mode exits

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 4. Shell Escape

**Test:** Verify `!cmd` executes shell commands within conversation.

**Steps:**
1. Enter conversation mode
2. At the `║` prompt, type: `!ls`
3. Press Enter

**Expected:**
- Command executes immediately
- Output appears (directory listing)
- Returns to `║` prompt after command completes
- Conversation mode remains active
- Hints still visible

**Advanced test:**
```
!echo "test" > /tmp/testfile
!cat /tmp/testfile
!rm /tmp/testfile
```

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 5. Exit via Escape Key

**Test:** Verify Esc key exits conversation mode.

**Steps:**
1. Enter conversation mode
2. Press `Esc` key

**Expected:**
- Conversation mode exits immediately
- Background tint disappears
- Normal shell prompt appears
- Shell is ready for next command

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 6. Exit via /done Command

**Test:** Verify `/done` command exits conversation mode.

**Steps:**
1. Enter conversation mode
2. Type: `/done`
3. Press Enter

**Expected:**
- Conversation mode exits
- Background tint disappears
- Normal shell prompt appears

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 7. Ctrl+C During Streaming

**Test:** Verify Ctrl+C during agent response.

**Steps:**
1. Enter conversation mode
2. Send a message that will have a long response
3. Press Ctrl+C while agent is streaming

**Expected:**
- Streaming stops
- Error message: "hash: request canceled"
- Conversation mode remains active
- Returns to `║` prompt (user can continue or exit)

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 8. Ctrl+C at Input Prompt

**Test:** Verify Ctrl+C at the conversation input prompt.

**Steps:**
1. Enter conversation mode
2. At the `║` prompt (not during streaming), press Ctrl+C

**Expected:**
- Conversation mode exits
- Returns to normal shell prompt

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 9. Pipe Mode Unchanged

**Test:** Verify pipe mode still works in single-turn (no conversation).

**Steps:**
1. Run: `ls | ?? what files are here`

**Expected:**
- Agent receives ls output as context
- Agent responds with analysis
- Single-turn confirmation UI appears (not conversation mode)
- Pipe mode never triggers conversation mode, even if marker present

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 10. Empty Input Handling

**Test:** Verify empty input in conversation mode.

**Steps:**
1. Enter conversation mode
2. At the `║` prompt, press Enter without typing anything

**Expected:**
- Nothing is sent to agent
- `║` prompt appears again
- Hints appear again
- Conversation mode remains active

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 11. Visual Verification

**Test:** Verify visual appearance of conversation mode.

**Check:**
- [ ] Background tint is subtle (not overwhelming)
- [ ] Tint color matches shell theme accent color
- [ ] `║` prompt is clearly visible in accent color
- [ ] Hints are visible but not distracting (dim gray)
- [ ] Markdown rendering works (bold, code blocks, lists)
- [ ] Text is readable with tint background

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

## Edge Cases

### 12. Multiple Conversation Turns

**Test:** Verify extended multi-turn conversation.

**Steps:**
1. Enter conversation mode
2. Exchange 5+ messages with the agent
3. Use a mix of text replies and shell escapes

**Expected:**
- All turns work correctly
- Background tint persists
- No memory leaks or performance degradation

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

### 13. Long Agent Responses

**Test:** Verify conversation mode with very long responses.

**Steps:**
1. Ask agent for a long explanation (e.g., "explain how TCP/IP works in detail")
2. Agent should include `[AWAITING_INPUT]` at the end

**Expected:**
- Full response streams correctly
- Markdown rendering works throughout
- Scrolling works properly
- Returns to `║` prompt after complete response

**Status:** ⬜ Not tested / ✅ Pass / ❌ Fail

---

## Known Limitations

Document any discovered limitations here:

-

## Issues Found

Document any bugs or issues discovered during testing:

-

## Testing Notes

Date tested: ___________
Tester: ___________
Agent used: ___________
Terminal: ___________
OS: ___________

Additional observations:

