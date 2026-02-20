# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- TODO.md documenting planned improvements and feature ideas
- Contributing section in README.md linking to TODO.md
- Process tagging system with persistence and per-host cache
  - Rules are now loaded from a private local config file (`~/.config/cgpus/tag-rules.sh` by default)
  - Public repo no longer hardcodes user/workload-specific tag logic
  - `MIXED` and `NONE` semantics unchanged
  - Tag summary now renders dynamically from observed tags
  - Individual GPU IDs in parentheses remain colorized by status
  - Row color remains based on worst status (red if any idle, yellow if only underutilized)
- `cgpus-zsh` backend with modularized probe/render flow
- `cgpus-go` backend (`cmd/cgpus-go`) with tests
- Launcher backend selection via `CGPUS_BACKEND=auto|zsh|go`
- Backend/build docs under `docs/`
- Last-tag cache persisted to `~/.cache/cgpus/last_tags.tsv` (or `$XDG_CACHE_HOME`)

### Changed
- `cgpus` is now a launcher that delegates to zsh or Go backend
- Refresh mode uses SSH multiplexing (`ControlMaster`, `ControlPersist`, `ControlPath`)
- Usage/help text standardized to `cgpus` command name

## [2.0.0] - 2026-02-05

### Added
- CPU utilization and host memory monitoring (`--cpu` flag)
- Dynamic column width based on actual data
- Historical sparkline visualization with color-coded bars
- Refresh mode with configurable interval (`-f` flag)
- Advanced GPU metrics (power, utilization, memory per GPU)
- Smart idle detection (multi-factor: power, memory, utilization)
- "Yellow" GPU detection for underutilized resources

### Changed
- Averages now computed over ALL GPUs, not just busy ones
- CPU percentage uses actual utilization (not load average)
- Memory displayed in TB with conditional decimal formatting

## [1.0.0] - 2024-09-18

### Added
- Initial release
- Basic GPU availability checking
- Simple threshold-based detection (50MB)
- Asynchronous SSH to multiple hosts
- Color-coded output (red for available)
