# TODO

## History / Suggestions

- [ ] Cross-shell freshness for the in-memory prefix index
  - Current: ghost-text suggestions come from a per-session index loaded once at
    store open, so commands run in another concurrently running shell only show
    up after a restart (explicit history search still queries SQLite live)
  - Goal: optional refresh via the upcoming hook system — e.g. an
    after-execution hook that re-runs `Store.loadPrefixIndex` so sessions pick
    up each other's commands
  - Note: `prefixIndex.install` already merges a reload with live session
    entries, so this only needs the trigger

## Prompt / Theming

- [ ] Fork jj-starship to customize colors to match telemetry theme
  - Current: jj-starship uses hardcoded ANSI colors
  - Goal: Match the `color_git_bg` (#20352A) and `color_fg0` (#e0f0ef) from starship.toml
  - Repo: https://github.com/dmmulroy/jj-starship
