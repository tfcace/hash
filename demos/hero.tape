# Hash Demo: AI-Powered Shell
# Run with: vhs demos/hero.tape (from the repo root, with a configured agent)
#
# This creates the main hero recording for the README and runhash.dev,
# showing the three core ?? forms: full request, pipe, inline completion.

Output demos/hero.gif
Output demos/hero.mp4
Output demos/hero.webm

Require hash

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 560
Set Theme "Catppuccin Mocha"
Set Padding 16
Set Framerate 24
Set TypingSpeed 55ms
Set WaitTimeout 60s

# Boot hash off-camera so the recording opens at the hash prompt
Hide
Type "hash"
Enter
Sleep 4s
Show

# Demo 1: natural language to command
Type "?? find all Go files modified today"
Sleep 800ms
Enter
Wait+Screen /Enter: run/
Sleep 1.5s
Enter
Sleep 2.5s

# Demo 2: pipe output through the agent
Type "cat go.mod | ?? list the main dependencies"
Sleep 800ms
Enter
Wait+Screen /Enter: done/
Sleep 3s
Enter
Sleep 1.5s

# Demo 3: inline completion fills one argument
Type "git log -3 --format=?? short hash and subject"
Sleep 800ms
Enter
Wait+Screen /Enter: run/
Sleep 1.5s
Enter
Sleep 2.5s

# Hold the final frame, exit off-camera
Sleep 3s
Hide
Type "exit"
Enter
