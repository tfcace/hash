# Hash Demo: AI-Powered Shell
# Run with: vhs demos/hero.tape
#
# This creates the main hero GIF for the README showing
# the core ?? functionality.

Output demos/hero.gif

Set Shell "bash"
Set FontSize 14
Set Width 900
Set Height 500
Set Theme "Catppuccin Mocha"
Set Padding 20
Set Framerate 30

# Start hash shell
Type "hash"
Enter
Sleep 2s

# Demo 1: Full agent request
Type "?? find all Go files larger than 100KB"
Sleep 500ms
Enter
Sleep 3s

# Show the generated command
Sleep 1s

# Press Enter to execute
Enter
Sleep 2s

# Demo 2: Pipe to agent
Type "cat go.mod | ?? list the main dependencies"
Sleep 500ms
Enter
Sleep 3s

# Demo 3: Inline completion
Type "git log --format=?? show hash and message only"
Sleep 500ms
Enter
Sleep 3s

# Execute the result
Enter
Sleep 2s

# Exit
Type "exit"
Enter
Sleep 500ms
