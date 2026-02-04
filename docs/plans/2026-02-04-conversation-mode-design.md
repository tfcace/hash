# Conversation Mode Design

## Overview

This document defines the design for multi-turn agent conversations in Hash. When the agent asks follow-up questions or expects user input, the shell enters a visually distinct "conversation mode" that allows natural back-and-forth dialogue.

## Visual Design

### Background Tint

The conversation zone uses a subtle background tint to visually distinguish it from normal shell output:

- Derived from accent color at ~10-15% opacity, or a soft dark shade like `#1e1e2e`
- Applied per-line using ANSI 24-bit background: `\x1b[48;2;R;G;Bm`
- Fallback for 256-color terminals: nearest dark shade

### Layout

```
┌─────────────────────────────────────────────────── tinted background
│
│ I can help you check what you have running on Fly.io. Let me use
│ the `flyctl` command to list your apps.
│
│ ┌────────────────────────────────────────────────
│ │ Agent wants to run:
│ │ flyctl apps list
│ │
│ │ [y]allow  [n]deny  [a]always allow
│ └────────────────────────────────────────────────
│
│ ✓ flyctl apps list
│
│ You have one app running on Fly.io:
│
│ - **jj-for-gitters** (personal)
│   - Status: deployed
│   - Latest deploy: Feb 2 2026 21:46
│
│ Would you like me to get more details about this app?
│
│ ║ _
│                                         Esc exit · !cmd shell
└───────────────────────────────────────────────────
```

### Key Visual Elements

| Element | Description |
|---------|-------------|
| Background tint | Wraps all content in conversation zone |
| `║` | Input prompt in accent color (double bar distinguishes from permission `│`) |
| Permission box | Nested container with single bar, same as current UI |
| `✓`/`✗` | Tool execution feedback |
| Hint footer | Dim gray, shown only when input is active |

## Triggering Conversation Mode

### Agent Marker Protocol

The agent signals when it expects a follow-up by ending its response with `[AWAITING_INPUT]`:

**System prompt instruction:**
```
When your response invites or requires user input to continue (questions,
offering options, asking for confirmation), end your response with the
marker [AWAITING_INPUT] on its own line.

Do NOT include this marker when:
- Providing a complete answer or explanation
- Suggesting a command to run
- The conversation naturally concludes
```

**Response processing:**
```go
func processAgentResponse(text string) (display string, expectsInput bool) {
    const marker = "[AWAITING_INPUT]"
    trimmed := strings.TrimSpace(text)
    if strings.HasSuffix(trimmed, marker) {
        display = strings.TrimSuffix(trimmed, marker)
        display = strings.TrimSpace(display)
        return display, true
    }
    return text, false
}
```

### Mode-Specific Behavior

| Invocation | Marker present | Behavior |
|------------|----------------|----------|
| `?? prompt` | Yes | Enter conversation mode (tinted zone) |
| `?? prompt` | No | Single-turn: `[Enter: ok] [Tab: copy] [Esc]` |
| `cmd \| ?? prompt` | N/A | Always single-turn (unchanged) |
| `cmd ?? prompt` | N/A | Always single-turn, inline ghost text (unchanged) |

## Interaction Flow

### Entering Conversation Mode

1. User types `?? what do I have running on fly.io?` and presses Enter
2. Tinted zone appears immediately
3. Spinner shows: `⠋ Agent thinking...`
4. Agent streams response (tint active, no input prompt yet)
5. Response ends with `[AWAITING_INPUT]` (stripped from display)
6. Input prompt appears: `║ _` with hint footer

### Continuing the Conversation

1. User types reply: `║ yes, show me the machines`
2. Presses Enter
3. User's message stays visible (becomes part of conversation history)
4. Spinner shows, agent streams next response
5. If marker present: input prompt reappears
6. If no marker: show `[Enter: ok] [Tab: copy] [Esc]`, exit on action

### Shell Escape

Users can run shell commands without leaving conversation mode using `!` prefix:

```
│ Would you like me to get more details about this app?
│
│ ║ !flyctl status
│
│   App: jj-for-gitters
│   Status: deployed
│   ...
│
│ ║ _
│                                         Esc exit · !cmd shell
```

- Command executes immediately, output appears in the zone
- Output becomes context for the conversation (agent can see it if referenced)
- Input prompt returns after command completes

### Exiting Conversation Mode

- Press **Esc** at any time
- Type **/done** and press Enter
- Both methods: tint fades away, text remains as normal scrollback

## Keyboard Reference

| Key | State | Action |
|-----|-------|--------|
| `Enter` | awaiting_input | Submit reply to agent |
| `Esc` | any | Exit conversation mode |
| `!` | awaiting_input (line start) | Shell escape prefix |
| `y/n/a` | permission | Allow/deny/always tool |
| `Ctrl+C` | streaming | Cancel request, stay in conversation |
| `Ctrl+C` | awaiting_input | Exit conversation mode |
| `/done` | awaiting_input | Exit conversation mode |

## State Machine

```
                    ┌─────────────────────────────────────┐
                    │         ConversationState           │
                    ├─────────────────────────────────────┤
  ?? + marker       │                                     │
 ───────────────►   │  ACTIVE                             │
                    │   ├─ streaming (agent responding)   │
                    │   ├─ permission (tool prompt)       │
                    │   ├─ awaiting_input (your turn)     │
                    │   └─ executing_shell (!cmd)         │
                    │                                     │
                    └───────────────┬─────────────────────┘
                                    │ Esc or /done
                                    ▼
                    ┌─────────────────────────────────────┐
                    │  INACTIVE (normal shell)            │
                    └─────────────────────────────────────┘
```

### Sub-state Transitions

| From | To | Trigger |
|------|----|---------|
| streaming | awaiting_input | Agent finishes (with marker) |
| streaming | INACTIVE | Agent finishes (no marker) + user dismisses |
| awaiting_input | streaming | User submits reply |
| awaiting_input | executing_shell | User types `!cmd` |
| executing_shell | awaiting_input | Shell command completes |
| any | permission | Agent requests tool use |
| permission | streaming | User allows/denies |
| any | INACTIVE | User presses Esc or types /done |

### Sub-state Properties

| Sub-state | Input allowed | Hints shown | Tint |
|-----------|--------------|-------------|------|
| streaming | No (blocked) | No | Yes |
| permission | y/n/a only | Permission keys | Yes |
| awaiting_input | Full text | Yes | Yes |
| executing_shell | No (blocked) | No | Yes |

## Edge Cases

1. **Agent doesn't send marker** — Single-turn flow with `[Enter: ok] [Tab: copy] [Esc]`

2. **Multiple tool calls in sequence** — Each shows permission box, streaming resumes between them

3. **Shell escape fails** — Error shown in zone, input prompt returns

4. **Long conversations** — Scrollback works normally; tint only applies to visible lines during active conversation (scrollback loses tint, text preserved)

5. **Terminal resize** — Re-render visible portion with tint

6. **Ctrl+C during streaming** — Cancel request, show `(canceled)`, return to awaiting_input

7. **Ctrl+C at awaiting_input** — Exit conversation mode (same as Esc)

## Implementation Notes

### Rendering

- Background tint applied per-line: `\x1b[48;2;R;G;Bm...content...\x1b[0m`
- Input prompt: `\x1b[38;2;R;G;Bm║\x1b[0m ` (accent-colored double bar)
- Hint footer: `\x1b[90mEsc exit · !cmd shell\x1b[0m`

### On Exit

1. Redraw all conversation lines without background tint
2. Preserve text styling (bold, colors, etc.)
3. Print fresh shell prompt below

### Files Affected

- `internal/shell/shell.go` — Conversation state management
- `internal/shell/agent_output.go` — Extend coordinator for conversation mode
- `internal/shell/response_ui.go` — Tinted zone rendering
- `internal/agent/acp.go` — Strip `[AWAITING_INPUT]` marker, return flag
- `internal/agent/handler.go` — Update system prompt with marker instruction
