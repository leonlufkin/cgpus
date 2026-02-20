# cgpus - GPU Monitoring Tool

Monitor NVIDIA GPU availability, utilization, and power across multiple remote hosts via SSH.

## Features

### cgpus (v2)
- Advanced metrics: GPU utilization, power draw, and memory per GPU
- Refresh mode with configurable interval (`-f [INTERVAL]`)
- Optional CPU and host memory stats (`--cpu`)
- Sparkline history with color coding
- Private process tagging via local rules file (not committed to repo)
- Last-tag cache persisted across runs (`~/.cache/cgpus/last_tags.tsv`)
- Dynamic layout based on terminal width
- SSH multiplexing in refresh mode to reduce connection overhead

### Backends
- `cgpus-zsh`: shell implementation with no compile step
- `cgpus-go`: Go implementation (compiled to `.bin/cgpus-go` on first run)
- `cgpus`: launcher that selects backend based on `CGPUS_BACKEND`

### Legacy
- `legacy/cgpus0`: simple one-shot availability checker

## Prerequisites

### Local machine
- `zsh`
- SSH key-based access to target hosts
- Optional: Go toolchain (`go`) if you want to build/use Go backend

### Remote hosts
- Linux hosts with `nvidia-smi`
- Standard utilities used by probe logic (`awk`, `ps`, `/proc`)

## Installation

1. Clone the repository:

```bash
git clone https://github.com/leonlufkin/cgpus.git
cd cgpus
```

2. Install launchers in your PATH:

```bash
cp cgpus ~/.local/bin/
cp cgpus-zsh ~/.local/bin/
cp cgpus-go ~/.local/bin/
chmod +x ~/.local/bin/cgpus ~/.local/bin/cgpus-zsh ~/.local/bin/cgpus-go
```

3. Optional: install legacy command:

```bash
cp legacy/cgpus0 ~/.local/bin/
chmod +x ~/.local/bin/cgpus0
```

4. Configure host groups:

```bash
cp examples/ssh_key_groups.sh.example ~/.ssh/ssh_key_groups.sh
$EDITOR ~/.ssh/ssh_key_groups.sh
```

5. Optional: configure private process tags:

```bash
mkdir -p ~/.config/cgpus
cp examples/tag-rules.sh.example ~/.config/cgpus/tag-rules.sh
$EDITOR ~/.config/cgpus/tag-rules.sh
```

## Configuration

Create `~/.ssh/ssh_key_groups.sh`:

```bash
#!/bin/zsh
typeset -A GROUPS=(
  my-cluster   "gpu-node-1 gpu-node-2 gpu-node-3"
  test-nodes   "test-gpu-a test-gpu-b"
)
```

Optional private tag rules:

- Default path: `~/.config/cgpus/tag-rules.sh`
- Override path: `CGPUS_TAG_RULES_FILE=/path/to/tag-rules.sh`
- Contract:
  - file should be Bash-compatible (`cgpus` executes it in remote `bash`)
  - define `CGPUS_TAG_ORDER=(...)`
  - define `cgpus_tag_rule tag owner proc_name cmd_line`
  - a tag is applied only when every GPU process on that host matches exactly one tag rule

## Backend Selection

Select backend with `CGPUS_BACKEND`:

- `auto` (default): prefer Go backend when available, otherwise zsh
- `go`: force Go backend
- `zsh`: force zsh backend

Examples:

```bash
CGPUS_BACKEND=auto cgpus my-cluster
CGPUS_BACKEND=go cgpus -f 5 my-cluster
CGPUS_BACKEND=zsh cgpus --cpu my-cluster
```

Note: `cgpus-go` builds a cached binary at `.bin/cgpus-go` when sources change.

## Usage

Basic:

```bash
cgpus my-cluster
```

With CPU stats:

```bash
cgpus --cpu my-cluster
```

Refresh every 10 seconds:

```bash
cgpus -f 10 my-cluster
```

Combined:

```bash
cgpus --cpu -f 5 my-cluster
```

## Refresh Performance

In refresh mode, SSH is invoked with multiplexing options:

- `ControlMaster=auto`
- `ControlPersist=600`
- `ControlPath=~/.ssh/cgpus-%C`

This avoids a full SSH handshake on each refresh iteration after the first connection.

## Contributing

Contributions are welcome. See `TODO.md` and docs in `docs/`.

## License

MIT License - see `LICENSE`.
