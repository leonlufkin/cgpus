# cgpus Architecture

## Overview

`cgpus` is a launcher that delegates to one of two backends:

- `cgpus-zsh`: shell backend
- `cgpus-go`: Go backend (wrapper that builds/runs `.bin/cgpus-go`)

Backend selection is controlled by `CGPUS_BACKEND=auto|zsh|go`.

## Data Flow

1. Parse CLI args (`--cpu`, `-f`, interval, groups).
2. Load group definitions from `~/.ssh/ssh_key_groups.sh` (`GROUPS` map).
3. Expand selected groups into an ordered, deduplicated host list.
4. Probe each host over SSH concurrently.
5. Parse normalized probe record.
6. Apply tag persistence and update per-host history.
7. Render rows and tag summary.
8. In refresh mode, repeat with interval pacing.

## Probe Record Contract

Each host probe returns one tab-delimited line.

### Success

`OK\tidle\ttotal\tutil\tpower\tmem_used\tmem_total\tyellow\tprocess_tag\tcpu_pct\thost_mem_used_tb\thost_mem_total_tb`

### Error

`ERR\treason`

Current reasons include:

- `ssh_fail`
- `ssh_timeout`
- `nvidia_missing`
- `parse_error`
- `empty`

## Tag Rules

Tags are assigned only when all observed GPU processes match exactly one rule:

- `*`: owner is `leon`
- `†`: owner is `kamesh`
- `‡`: owner is `cutter`
- `RL`: process name contains `ray::WorkerDict` or owner is `pritish`
- `CM`: command line contains `cmoe`
- `AU`: command line contains `lisan.al_gaib` or `zonos`
- `DA`: owner is `xiao` or command line contains `dataInfra`
- `AR`: command line contains `pretrain_gpt`

If multiple tags match all processes, tag is `MIXED`.
If no tags match all processes, tag is `NONE`.
`MIXED` and `NONE` clear persisted tag state.

## Refresh Transport

In refresh mode, SSH multiplexing is enabled:

- `ControlMaster=auto`
- `ControlPersist=600`
- `ControlPath=~/.ssh/cgpus-%C`

This reduces repeated handshake overhead during continuous polling.

## Build Notes

- Go sources live under `cmd/cgpus-go`.
- `cgpus-go` wrapper compiles to `.bin/cgpus-go` when required.
- `.bin/` is ignored in git.

## Tag Cache

- Last tags are cached per host in `~/.cache/cgpus/last_tags.tsv` (or `$XDG_CACHE_HOME/cgpus/last_tags.tsv`).
- Cache is loaded on startup and refreshed after each snapshot, so restart preserves tag memory.
