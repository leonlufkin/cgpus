# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- TODO.md documenting planned improvements and feature ideas
- Contributing section in README.md linking to TODO.md

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
