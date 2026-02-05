# TODO

## Performance
- Use SSH ControlMaster/ControlPersist to reuse connections in refresh mode (currently reconnects every iteration)
  - Current behavior: Creates new SSH connection on every refresh (~100ms overhead per host)
  - Proposed solution: Add `-o ControlMaster=auto -o ControlPath=~/.ssh/cm-%r@%h:%p -o ControlPersist=10m` to SSH command
  - Expected improvement: ~90% reduction in connection overhead (5-10ms per host after first connection)
  - Implementation: Modify SSH command in `check_gpus()` function around line 164

## Features
- Connection status indicators (green/yellow/red dots for host health)
- `--log` flag to append results to file with timestamps
- Config file support (~/.cgpusrc) for default hosts and settings instead of shell script

## Documentation
- Add screenshots/GIFs to README showing typical usage
- Add troubleshooting section for common SSH issues
- Document performance characteristics and scalability limits
