# TODO

## Performance
- Add bounded worker pool in zsh backend to avoid overload on very large clusters
- Add CLI flag for per-host probe timeout controls
- Add lightweight metrics for refresh latency per cycle

## Features
- Connection status indicators (green/yellow/red dots for host health)
- `--log` flag to append results to file with timestamps
- Config file support (`~/.cgpusrc`) for defaults
- Optional JSON output mode for downstream tooling

## Backend
- Add release build pipeline for precompiled `cgpus-go` binaries
- Add parity test fixtures comparing zsh and Go outputs for fixed probe inputs
- Add fallback behavior tests for launcher backend selection

## Documentation
- Add screenshots/GIFs to README showing typical usage
- Add troubleshooting section for common SSH and `nvidia-smi` issues
- Document performance/scaling characteristics and limits
- Keep `docs/streaming-feasibility.md` updated if transport model changes
