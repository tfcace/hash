# Hash Demo Recordings

This directory contains [VHS](https://github.com/charmbracelet/vhs) tape files for generating terminal demo GIFs.

## Prerequisites

Install VHS (requires ffmpeg and ttyd):

```bash
# macOS
brew install vhs

# Go install
go install github.com/charmbracelet/vhs@latest

# Or download from releases
# https://github.com/charmbracelet/vhs/releases
```

## Generating Demos

Generate all demos:

```bash
for tape in demos/*.tape; do
  vhs "$tape"
done
```

Generate a specific demo:

```bash
vhs demos/hero.tape
```

## Available Demos

| Demo | Description | Output |
|------|-------------|--------|
| `hero.tape` | Main feature showcase for README | `hero.gif` |
| `pipe-completion.tape` | Piping output through agent | `pipe-completion.gif` |
| `learning.tape` | Adaptive error fix learning | `learning.gif` |
| `context-picker.tape` | Ctrl+P context selection TUI | `context-picker.gif` |

## Customization

Each tape file can be customized:

- `Set Theme "..."` - Change color scheme (see [VHS themes](https://github.com/charmbracelet/vhs#settings))
- `Set FontSize N` - Adjust font size
- `Set Width/Height N` - Change dimensions
- `Sleep Ns` - Adjust timing between commands

## CI Integration

These demos can be auto-generated in CI. See `.github/workflows/demos.yml` (if enabled).

## Contributing

When adding new features, consider creating a demo tape to showcase them:

1. Create a new `.tape` file in this directory
2. Follow the existing naming convention
3. Keep demos focused (30-60 seconds max)
4. Test locally before committing
5. Add to the table above
