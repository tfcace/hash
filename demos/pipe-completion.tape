# Hash Demo: Pipe to Agent
# Run with: vhs demos/pipe-completion.tape
#
# Demonstrates piping command output through the AI agent
# for filtering, transformation, and analysis.

Output demos/pipe-completion.gif

Set Shell "bash"
Set FontSize 14
Set Width 900
Set Height 450
Set Theme "Catppuccin Mocha"
Set Padding 20
Set Framerate 30

# Start hash
Type "hash"
Enter
Sleep 1.5s

# Example 1: Filter JSON output
Type "kubectl get pods -o json | ?? extract just the pod names"
Sleep 500ms
Enter
Sleep 3s

# Show the jq command it generates
Sleep 1s
Enter
Sleep 2s

# Example 2: Summarize log output
Type "docker logs myapp --tail 50 | ?? summarize any errors"
Sleep 500ms
Enter
Sleep 3s

# Example 3: Transform data format
Type "cat data.csv | ?? convert to markdown table"
Sleep 500ms
Enter
Sleep 3s

# Exit
Type "exit"
Enter
Sleep 500ms
