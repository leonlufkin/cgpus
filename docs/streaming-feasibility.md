# Streaming Feasibility Notes

## Context

Current refresh mode is pull-based: each cycle probes every host over SSH. With multiplexing enabled, the SSH handshake cost is mostly amortized after the first cycle.

## Considered Options

### 1. SSH Multiplexed Polling (current)

Pros:
- Minimal operational complexity
- No remote installation changes
- Easy failure recovery per cycle
- Works with existing access model

Cons:
- Still runs a probe command each interval
- Some per-cycle process overhead remains

### 2. Long-lived SSH stream per host

Model:
- Keep one remote shell process per host emitting snapshots periodically.

Pros:
- Lower per-cycle command startup overhead
- Lower jitter at very short intervals

Cons:
- More complex lifecycle management
- Harder error handling and resync semantics
- More brittle when terminals/network flap

### 3. Remote daemon/agent on each host

Model:
- Install/manage persistent service that pushes or serves stats.

Pros:
- Lowest polling overhead
- Can support richer telemetry

Cons:
- Deployment and security burden
- Requires host-side installation/operations model
- More moving parts than needed for current scope

## Decision for Current Refactor

Use SSH multiplexed polling. It gives most of the practical latency improvement with the lowest complexity and operational risk.

## Revisit Conditions

Reconsider streaming/agent model if:

- Poll interval needs to be <2s across many hosts
- Host count scales beyond current tolerance
- Additional continuous telemetry is required
- There is appetite for managing host-side services
