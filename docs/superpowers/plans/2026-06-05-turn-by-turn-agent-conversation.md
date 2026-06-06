# Turn-by-Turn Agent Conversation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make full `??` agent interactions continue turn-by-turn when the assistant asks for input, without relying on `[AWAITING_INPUT]`.

**Architecture:** Add marker stripping and follow-up detection as small shell helpers. Add a reply-capable explanation confirmation path and a lightweight conversation loop that streams follow-up prompts through the current ACP session or a transcript prompt for stateless transports.

**Tech Stack:** Go, existing shell editor, existing agent `Transport` and `Client`, existing streaming markdown renderer.

---

### Task 1: Response Cleanup And Follow-Up Detection

**Files:**
- Create: `internal/shell/agent_conversation.go`
- Create: `internal/shell/agent_conversation_test.go`
- Modify: `internal/shell/agent_stream_engine.go`

- [ ] Write failing tests for legacy marker stripping and conservative follow-up detection.
- [ ] Run `go test ./internal/shell/... -run 'TestAgentConversation|TestCollectAgentStream' -v` and confirm the new tests fail.
- [ ] Implement a streaming sanitizer that strips `[AWAITING_INPUT]` and `[CONVERSATION]`, including split chunks.
- [ ] Implement `agentResponseWantsReply`.
- [ ] Re-run the focused tests and confirm they pass.

### Task 2: Follow-Up Request Construction

**Files:**
- Modify: `internal/agent/types.go`
- Modify: `internal/shell/agent_handler.go`
- Modify: `internal/shell/agent_handler_test.go`

- [ ] Write failing tests that ACP follow-ups send only the latest reply and HTTP/mock follow-ups include a compact transcript.
- [ ] Run `go test ./internal/shell/... -run 'TestBuildFollowUp|TestAgentHandler' -v` and confirm failure.
- [ ] Add `Client.Name()` and follow-up request helpers in `AgentHandler`.
- [ ] Re-run focused tests and confirm they pass.

### Task 3: Reply-Capable Confirmation UI

**Files:**
- Modify: `internal/shell/response_ui.go`
- Modify: `internal/shell/agent_output.go`
- Modify: `internal/shell/agent_output_test.go`
- Modify: `internal/shell/shell_test.go`

- [ ] Write failing tests for explanation confirmation showing a reply action and mapping explanation responses to confirmation when reply is allowed.
- [ ] Run `go test ./internal/shell/... -run 'TestAgentOutputCoordinator_ShowHints|TestConfirmationType' -v` and confirm failure.
- [ ] Add `ConfirmReply`, `r`/`R` handling for explanation confirmations, and updated hints.
- [ ] Re-run focused tests and confirm they pass.

### Task 4: Shell Conversation Loop

**Files:**
- Modify: `internal/shell/shell.go`
- Modify: `internal/shell/agent_handler.go`
- Modify: `internal/shell/agent_stream_engine_test.go`

- [ ] Write failing tests around turn-loop decisions where an assistant question triggers continuation and non-question explanations expose explicit reply.
- [ ] Run `go test ./internal/shell/... -run 'TestAgentConversation|TestHandleAgent' -v` and confirm failure.
- [ ] Refactor the streaming turn collection into a reusable helper.
- [ ] Add a lightweight reply prompt using the existing editor with `you> ` as the prompt.
- [ ] Loop over follow-up turns until the user exits, the response no longer asks for input, or a command response enters the existing run/edit/cancel flow.
- [ ] Re-run focused tests and then `go test ./internal/shell/... ./internal/agent/...`.

### Task 5: Full Verification

**Files:**
- Modify docs only if behavior notes need updating.

- [ ] Run `go test ./...`.
- [ ] Run `go test -race ./internal/shell/... ./internal/agent/...` if the full suite is clean.
- [ ] Run `go build ./cmd/hash`.
