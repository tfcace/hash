# Hash Demo: Adaptive Learning
# Run with: vhs demos/learning.tape
#
# Shows how Hash learns from your error fixes and
# suggests them automatically in the future.

Output demos/learning.gif

Set Shell "bash"
Set FontSize 14
Set Width 900
Set Height 500
Set Theme "Catppuccin Mocha"
Set Padding 20
Set Framerate 30

# Start hash
Type "hash"
Enter
Sleep 1.5s

# First: Run a script without execute permission
Type "./deploy.sh"
Enter
Sleep 1s

# Shows: Permission denied error with learned fix suggestion
# The prompt shows:
#   ✗ Exit 126 │ Learned fix available
#   → chmod +x ./deploy.sh (worked 12 times)
#   [Enter: run] [Tab: edit] [?: ask agent]

Sleep 2s

# Press Enter to apply the learned fix
Enter
Sleep 1.5s

# Now the script runs
Sleep 2s

# Another example: command not found
Type "pythno script.py"
Enter
Sleep 1s

# Shows typo correction suggestion
Sleep 2s

# Press Enter to apply
Enter
Sleep 1.5s

# Exit
Type "exit"
Enter
Sleep 500ms
