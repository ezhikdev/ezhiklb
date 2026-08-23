# EzhikLB

EzhikLB is a lightweight Linux control plane and node agent for kernel-level
TCP and UDP load balancing with IPVS.

The first alpha focuses on a single-host `Panel + Node` deployment while using
the same profile/revision model that remote nodes will use later.

## Alpha capabilities

- Reusable configuration profiles assigned to nodes.
- Immutable desired revisions and reported actual revisions.
- TCP, UDP, or dual-protocol listeners.
- Multiple backends with weighted round-robin.
- Optional source affinity; disabled by default.
- Global ICMP reachability checks with failure and recovery thresholds.
- Incremental IPVS updates without clearing unrelated services.
- React administration panel and JSON HTTP API.
- Installer roles: Panel, Node, Panel + Node, and Upgrade.

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

