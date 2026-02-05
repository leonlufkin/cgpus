# cgpus - GPU Monitoring Tool

Monitor NVIDIA GPU availability, utilization, and performance across multiple remote hosts via SSH.

## Features

### cgpus (v2.0) - Enhanced Version
- **Advanced Metrics**: GPU utilization, power draw, memory per GPU
- **Refresh Mode**: Continuous monitoring with configurable intervals
- **CPU Monitoring**: Optional CPU utilization and host memory stats
- **Sparkline History**: Visual power consumption history with color coding
- **Smart Coloring**: Red (idle GPUs), Yellow (underutilized), White (busy)
- **Dynamic Layout**: Auto-adjusting column widths based on data
- **Smart Detection**: Multi-factor idle GPU detection

### cgpus0 (v1.0) - Legacy Version
- **Simple & Fast**: Basic GPU availability checking
- **Lightweight**: ~70 lines of code
- **Color Output**: Red for available GPUs
- **One-shot**: Quick checks without continuous monitoring

## Installation

### Prerequisites
- `zsh` shell
- NVIDIA GPU tools (`nvidia-smi`) on remote hosts
- SSH with key-based authentication
- For cgpus v2: `bc`, `top`, `free`, `awk`

### Steps
1. Clone the repository:
   ```bash
   git clone https://github.com/leonlufkin/cgpus.git
   cd cgpus
   ```

2. Copy scripts to your PATH:
   ```bash
   # Enhanced version (recommended)
   cp cgpus ~/.local/bin/
   chmod +x ~/.local/bin/cgpus

   # Legacy version (optional)
   cp legacy/cgpus0 ~/.local/bin/
   chmod +x ~/.local/bin/cgpus0
   ```

3. Configure SSH groups:
   ```bash
   # Copy example configuration
   cp examples/ssh_key_groups.sh.example ~/.ssh/ssh_key_groups.sh

   # Edit with your host groups
   vim ~/.ssh/ssh_key_groups.sh
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

## Usage

### cgpus (v2.0)

**Basic usage:**
```bash
cgpus my-cluster
```

**With CPU monitoring:**
```bash
cgpus --cpu my-cluster
```

**Refresh mode (updates every 10 seconds):**
```bash
cgpus -f 10 my-cluster
```

**Combined:**
```bash
cgpus --cpu -f 5 my-cluster
```

**Example output:**
```
vp2:   0/8*     100%   590.2W   59.4/80 GB                8%  0.1/2 TB   ▇▇▇
vp11:  3/7       25%   262.5W   36.3/80 GB  (2-6)       2.2%  0.4/2 TB   ▃▃▃
vp44:  8/8        0%    70.3W    0.0/80 GB  (0-7)       0.3%  0.0/2 TB
```

### cgpus0 (v1.0)

**Basic usage:**
```bash
cgpus0 my-cluster
```

**Example output:**
```
gpu-node-1: 2/8 GPUs available
gpu-node-2: 0/8 GPUs available
gpu-node-3: 4/8 GPUs available
```

## Feature Comparison

| Feature | cgpus0 (v1.0) | cgpus (v2.0) |
|---------|---------------|--------------|
| GPU Availability | Yes | Yes |
| Power Draw | No | Yes |
| GPU Utilization | No | Yes |
| Memory per GPU | No | Yes |
| CPU Monitoring | No | Yes (--cpu) |
| Host Memory | No | Yes (--cpu) |
| Refresh Mode | No | Yes (-f) |
| Sparkline History | No | Yes |
| Color-coded History | No | Yes |
| Dynamic Layout | No | Yes |
| Lines of Code | ~70 | ~550 |

## License

MIT License - see [LICENSE](LICENSE) file for details.
