# Hash Demo: Context Picker (Ctrl+P)
# Run with: vhs demos/context-picker.tape
#
# Shows the interactive context picker that lets you
# control exactly what context goes to the AI agent.

Output demos/context-picker.gif

Set Shell "bash"
Set FontSize 14
Set Width 900
Set Height 550
Set Theme "Catppuccin Mocha"
Set Padding 20
Set Framerate 30

# Start hash
Type "hash"
Enter
Sleep 1.5s

# Run a few commands to build up history
Type "kubectl get pods -n staging"
Enter
Sleep 1s

Type "docker ps --format '{{.Names}}'"
Enter
Sleep 1s

# Now open context picker before asking agent
Ctrl+P
Sleep 1s

# Navigate through the TUI
# Shows categories: History, Environment, etc.
Down
Sleep 500ms
Down
Sleep 500ms

# Toggle some items with Space
Space
Sleep 500ms
Down
Space
Sleep 500ms

# Press Enter to confirm
Enter
Sleep 1s

# Now ask the agent with selected context
Type "?? why is my staging pod crashing"
Sleep 500ms
Enter
Sleep 3s

# The agent has the kubectl and docker context
Sleep 2s

# Exit
Type "exit"
Enter
Sleep 500ms
