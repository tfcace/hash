# Interactive History Search (Ctrl+R)

Hash includes a powerful interactive history search feature, inspired by the `reverse-i-search` functionality found in Bash and other Unix shells. Press **Ctrl+R** at any time to open the search interface on its **Commands** tab. Press **Tab** to select saved Agent results or **Shift+Tab** to return to commands when `[history].agent_results_enabled = true`.

## Quick Start

### Basic Usage

1. Press **Ctrl+R** to open the search interface
2. Type keywords to search through your command history in real-time
3. Navigate results using arrow keys or Ctrl+N/Ctrl+P
4. Press **Enter** to insert a command at the prompt, then review or edit it before execution
5. Press **Esc** or **Ctrl+C** to cancel

### Example Session

```
$ echo hello
hello
$ ls -la /tmp
[directory listing]
$ pwd
/home/user
$ Ctrl+R
(reverse-i-search): ls
  ls -la /tmp
  2026-01-01 12:34 [main]
  pwd
  2026-01-01 12:30 [dev]
[Enter to select]
```

## Features

### Full-Text Search
- Search across your entire command history
- Case-insensitive matching by default
- Results appear in real-time as you type
- Non-matching results are immediately filtered out

### Rich Metadata Display
Each command in the search results displays:
- **Command**: The actual command text (truncated if longer than terminal width)
- **Timestamp**: When the command was executed (YYYY-MM-DD HH:MM format)
- **Git Branch**: If available, shows the git branch active at execution time
- **Exit Code**: If non-zero, displays the exit code (e.g., `x1` for exit code 1)

Example:
```
> kubectl get pods
  2026-01-01 15:45 [main]
  docker ps
  2026-01-01 15:40 [dev] x1
```

### Navigation
- **Up Arrow / Ctrl+P**: Select previous result
- **Down Arrow / Ctrl+N**: Select next result
- **Tab / Shift+Tab**: Select the Agent results or Commands tab
- **Enter**: Insert the selected command at the prompt; it is never executed by the history picker
- **Esc / Ctrl+C**: Cancel and return to normal prompt

### Real-Time Filtering
As you type, the search results update in real-time:
- Type more characters to narrow results
- Use backspace to broaden results
- The selected result automatically resets when search query changes

### Agent Results

When `agent_results_enabled = true`, use the visible **Agent results** tab with
**Tab** to search saved full, pipe, and conversation agent turns without losing the current query.
Use **Shift+Tab** to return to Commands. Agent rows show the prompt,
response kind, timestamp, and acceptance status. Only command-kind responses can be
inserted with **Enter**; explanations and legacy unknown responses remain preview and
copy-only. Use **Ctrl+Y** to copy any selected agent response.

## Advanced Usage

### Searching for Specific Command Patterns

#### Search by Command Name
```
Ctrl+R
(reverse-i-search): docker
```
Shows all commands containing "docker"

#### Search by Arguments
```
Ctrl+R
(reverse-i-search): get pods
```
Shows commands containing "get pods" (useful for kubectl)

#### Search for Error Recovery
```
Ctrl+R
(reverse-i-search): grep
```
Find previous grep commands, insert one, and adjust its arguments before running it

### Combining with Other Features

#### With Piping
You can take a searched command and pipe it to another:
```
$ Ctrl+R → select "ls -la /var/log" → Enter
$ | less
```

#### With Command Modification
After selecting a command, you can edit it before execution:
```
$ Ctrl+R → select "docker ps" → Enter
$ docker ps -a  # Edit before running
```

## Tips and Tricks

### Most Recent First
Search results are ordered by recency - the most recent matching command appears first. This helps you quickly find commands you've used recently.

### Using Full Paths
If you can't remember the exact command, search for parts of it:
- Search `kubectl` to find all kubernetes commands
- Search `grep` to find all filter commands
- Search `python` to find all Python script executions

### Clearing History
If you want to remove sensitive commands from history, see the History Management section in the main documentation.

### Empty Query
If you press Ctrl+R and immediately start navigating without typing, you'll see your most recent commands. This is useful for finding what you just ran.

## Limitations and Behavior

### Terminal Width Handling
Long commands are automatically truncated to fit your terminal width. The truncated portion is replaced with "..." to indicate omission. The full command is still executed - truncation is only for display.

### Result Limits
The search interface shows up to 10 results at a time. If you have many matching commands, refine your search to narrow down the results.

### Unicode Support
The search interface fully supports Unicode characters:
- Search for commands containing non-ASCII text
- Results display correctly with emojis and international characters
- Terminal width calculations respect multi-byte characters

### Special Characters
Commands containing quotes, pipes, wildcards, and other special characters are preserved correctly:
- `echo "hello world"` - quotes preserved
- `grep 'pattern|with|pipes'` - pipes preserved
- `find . -name "*.txt"` - wildcards preserved
- `cmd | grep filter` - full pipe syntax available

## Error Handling

### No Results
If your search returns no matching commands, the UI shows an empty results area with just the header and footer. You can:
- Backspace to broaden your search
- Type different keywords
- Press Esc to cancel

### Terminal Resize
The search interface properly handles terminal resize events:
- Results reformat when terminal width changes
- Display updates immediately
- No data loss - search state is preserved

### Rapid Keypresses
The search interface is responsive to rapid keypresses:
- Multiple characters per keystroke are processed correctly
- Backspace and delete work as expected
- Navigation keys (arrows) work smoothly

## Performance

### Search Speed
- Local history search completes in <100ms for most queries
- Real-time filtering ensures responsive feedback
- No network latency - history is stored locally

### Database Size
History is stored in an SQLite database with unlimited entries. Even with thousands of commands:
- Search remains fast (<100ms for typical queries)
- No memory issues from large history
- Automatic cleanup of old entries is supported

## Integration with Other Shells

### As a Drop-in Replacement
Hash's Ctrl+R works similarly to other shells:
- Bash users will find familiar behavior
- Zsh and Fish users will recognize the pattern
- The experience is consistent across Unix-like shells

### Different from Bash
While inspired by Bash's reverse-i-search, Hash's version includes:
- Rich metadata display (timestamp, branch, exit code)
- Better Unicode support
- Modern TUI with color and styling

## See Also

- [Configuration Reference](../config-reference.md) - Learn how to customize keybindings
- [History Management](../features/history.md) - Learn about history storage and search options
- [Keybindings](../features/keybindings.md) - Customize your keybindings including Ctrl+R
