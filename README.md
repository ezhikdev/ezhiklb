# EzhikLB

EzhikLB is a lightweight Linux control plane and node agent for kernel-level
TCP and UDP load balancing with IPVS.

Alpha.6 supports both a single-host `Panel + Node` deployment and remote nodes
that receive reusable profile revisions from one panel.

## Alpha capabilities

- Reusable configuration profiles assigned to nodes.
- Immutable desired revisions and reported actual revisions.
- TCP, UDP, or dual-protocol listeners.
- Multiple backends with weighted round-robin.
- Optional source affinity with documented 15-minute through 24-hour presets and a custom seconds value.
- Global ICMP reachability checks with failure and recovery thresholds.
- Incremental IPVS updates without clearing unrelated services.
- React administration panel and JSON HTTP API.
- Compact Cloudflare-style rule list with focused per-rule editing.
- Inline conflict validation, cloning, weight percentages and unsaved-change protection.
- Live per-service and per-backend IPVS packet, byte and connection counters.
- Visible desired/applied state and backend ICMP status.
- Installer roles: Panel, Node, Panel + Node, and Upgrade.
- Per-node remote credentials, generated installation command and credential rotation.
- Profile cloning, guarded deletion, immutable revision history and rollback.

## Repository layout

```text
cmd/ezhiklb          Panel, API and embedded web application
cmd/ezhiklb-agent    Privileged local node agent
internal/domain      Shared desired-state model and validation
internal/store       SQLite persistence and immutable revisions
internal/api         HTTP API and authentication
internal/agent       IPVS, firewall and health reconciliation
web                  React + TypeScript panel
scripts              Installation and service templates
docs                 Architecture and test-node instructions
```

## Development status

This repository is an alpha. Do not deploy it on a production gateway before
running the test-node checklist in `docs/TESTING.md`.
