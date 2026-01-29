Top 10 UX Paper Cuts for Hash
1. No Loading States for Agent Requests
Location: internal/shell/shell.go:769-771, internal/shell/response_ui.go

Currently, agent requests show only a generic spinner with no context. Users can't distinguish between "connecting", "agent thinking", or "network timeout". For requests that can take 1-10+ seconds, this creates anxiety and uncertainty.

Fix: Multi-stage progress indicator showing: Connecting → Sending context → Agent thinking → Receiving response

2. Silent Failures Everywhere
Locations:

PTY fallback: internal/executor/executor.go:645-652
Starship fallback: internal/prompt/prompt.go:76-89
History save failures: internal/readline/readline.go:34-68
Config load failures: internal/config/config.go:135-140
Many critical subsystems silently degrade without any indication. Users think features are working when they're not (history not saving, Starship not loading, PTY not available).

Fix: Add one-time startup warnings for degraded features, and a /status or hash --status command to show what's working.

3. Output Truncation Without Warning
Locations:

Executor capture: internal/executor/executor.go:28-29, 551-561 (1MB limit)
Clipboard buffer: internal/clipboard/buffer.go:45-66
When command output exceeds 1MB, it's silently truncated for clipboard/agent context. Users ask "why didn't the agent see my output?" without understanding limits.

Fix: Display a subtle (output truncated: 1.2MB → 1MB) notice when truncation occurs.

4. Zero Feature Discoverability
Problem: Features like ??, cmd | ??, cmd ??, Ctrl+Y (copy command), Ctrl+O (copy output), Ctrl+R (history search), context picker are completely hidden.

Fix:

First-run welcome showing key features
hash help or hash tips command with feature overview
Occasional subtle hints: "Tip: Use ?? to ask the AI for help"
5. Error Handler Doesn't Show What Failed
Location: internal/shell/error_handler.go:49-57

When a command fails, users see only: "✗ Exit 126 | ?? to explain". The actual error (stderr) isn't shown inline—users must scroll up manually.

Fix: Show the last 2-3 lines of stderr in the error prompt so users understand what went wrong before deciding to ask the agent.

6. History Search UX Issues
Location: internal/history/search_ui.go

Can't view full command when truncated (:263-268)
[1/10] format is ambiguous—page or result number? (:229-238)
No way to preview command before selecting
Copy confirmation disappears after 1.5s (:383-414)
Fix: Add command preview on hover/selection, clear count format (result 1 of 10), persistent copy confirmation.

7. No Keybinding Indicator
Location: internal/shell/shell.go

Users with Emacs/Vim muscle memory don't know which keybinding mode is active. No visual indication of current keybinding style.

Fix: Show keybinding mode in prompt or status area: [helix], [emacs], [vim]

8. Unreliable Escape Key Detection
Location: internal/shell/response_ui.go:249-271

Uses 50ms timeout to detect standalone ESC vs. escape sequence. Under system load, this races and produces inconsistent behavior—sometimes ESC works, sometimes it's interpreted as part of a sequence.

Fix: Use proper terminal escape sequence parsing with buffered input, not timing-based heuristics.

9. Learned Fixes Hidden at 50-69% Confidence
Location: internal/shell/error_handler.go:33-37

Fixes with 0.5-0.69 confidence are silently discarded. Users miss potentially helpful suggestions just because they haven't been tried enough times yet.

Fix: Show lower-confidence fixes with visual distinction: "Possible fix (tried 2×): ..." vs "Suggested fix (worked 5×): ..."

10. Agent Invocation Confusion
Location: internal/parser/parser.go:139-156, internal/shell/shell.go:632-641

Multiple ?? patterns (line start, pipe, inline) with no feedback about which was matched. When agent is disabled, error appears after the prompt moves on—easy to miss.

Fix:

Show which mode was detected: "[agent: inline completion]"
When agent unavailable, block at invocation with clear error: "Agent not configured. Run 'hash config' to set up."
Honorable Mentions
Issue	Location	Impact
Context picker shows raw bytes	internal/context/ui.go:144	Minor confusion
Double Ctrl+C undocumented	internal/executor/executor.go:710-724	Frustrating
Config validation missing	internal/config/config.go:129-162	Silent misconfig
No undo for agent suggestions	internal/shell/shell.go:815-847	Workflow friction
Summary
The core themes are:

Silent failures → Add visible warnings and status checks
Missing feedback → Add loading states and progress indicators
Hidden features → Add discoverability and hints
Unclear state → Show mode/status indicators
Truncation/limits → Communicate constraints clearly
Fixing these 10 issues would transform Hash from "powerful but confusing" to "powerful and delightful."


Open in CLI

