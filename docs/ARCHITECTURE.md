# EzhikLB architecture

## State model

The panel stores mutable profile metadata and immutable profile revisions. A
node points to one profile and receives the latest published revision as its
desired state. The agent reports the revision it actually applied.

```text
Profile -> Revision 1
        -> Revision 2 (desired) <- Node A (actual: 2)
                              <- Node B (actual: 1)
```

Node-specific values such as the ingress address are not part of a reusable
profile. They live on the node and are merged with the selected profile by the
agent configuration endpoint.

## Data plane

Each enabled listener protocol becomes one IPVS virtual service. Backends use
IPVS NAT mode and may have a destination port different from the listen port.
The agent owns only services recorded in its state file and never calls
`ipvsadm -C`.

Reconciliation is differential:

1. Validate the complete desired revision.
2. Resolve routes and SNAT source addresses.
3. Prepare EzhikLB-owned firewall chains.
4. Add or update virtual services and destinations.
5. Quiesce and remove obsolete destinations.
6. Remove obsolete virtual services.
7. Persist the applied state and report the actual revision.

Source affinity is optional and defaults to zero. IPVS already keeps packets
within an existing flow on the same destination; affinity adds a broader
source-IP template and changes how weights are observed.

## Health checking

The agreed alpha health check is ICMP-only. It measures host reachability, not
application-port health. After `failure_threshold` consecutive failures the
agent sets every matching IPVS destination weight to zero. After
`recovery_threshold` consecutive successes it restores the configured weight.

The agent also configures `expire_nodest_conn` and
`expire_quiescent_template`, allowing new traffic to leave a quiescent backend.

## UDP idle timeouts

IPVS's own UDP connection timeout and the kernel's `nf_conntrack_udp_timeout*`
sysctls default to values (300s and ~120-180s respectively) shorter than a
realistic client idle period — a phone locked for a few minutes, for example.
This addresses the "Confirmed alpha.5 findings" UDP idle-resume report in
`docs/ROADMAP.md`.

The two timeouts are tuned *differently*, deliberately — an earlier attempt
extended both to 24h and caused a production incident (sustained 40-50%+ CPU
on busy nodes, conntrack approaching its limit), because IPVS's own
`ip_vs_conn` table then accumulated a full day of entries instead of a few
minutes, and this agent's own `MetricsCollector` scans that table every
heartbeat for the active-IP metric.

- **`nf_conntrack_udp_timeout_stream`** (`udpConntrackTimeoutSeconds`, 24h —
  the longest Affinity preset a listener can choose) is what actually needs
  to be long. `EZHIKLB-FORWARD`'s return-path rule only `ACCEPT`s a backend's
  reply once conntrack classifies the flow as `ESTABLISHED,RELATED`, and that
  classification depends solely on this timeout.
- **IPVS's own UDP connection timeout** (`udpIPVSTimeoutSeconds`, via
  `ipvsadm --set`) is kept short (300s, close to the kernel default) on
  purpose. It doesn't need to survive the client's idle period: a resumed
  flow is re-routed to the correct backend by the listener's own Affinity
  (persistence template, `-p <seconds>`) independently of whether the old
  `ip_vs_conn` entry still exists, so extending it only inflated a table
  without buying any correctness.
- **`nf_conntrack_max`** is raised (to 2,000,000) to give the long-lived
  conntrack table room, since entries now linger up to 24h instead of ~3
  minutes.

Both are set in `Reconciler.configureKernel` (`internal/agent/reconciler.go`);
this is global kernel state for the whole node (`ipvsadm --set` has no
per-listener granularity), not derived from any specific listener's own
Affinity value — Affinity itself still governs which backend a client is
routed back to.

## Security boundary

The panel runs unprivileged. Only the agent runs as root. The panel never sends
shell commands; it exposes validated structured desired state. The local agent
may use the installation-wide bootstrap token; each remote node receives its
own random credential and only its SHA-256 hash is stored by the panel. A new
credential is displayed once and rotation invalidates the previous value.

Remote nodes support both HTTP and HTTPS. Plain HTTP requires the explicit
`EZHIKLB_ALLOW_INSECURE=1` setting, which the panel adds to generated commands
automatically for `http://` URLs. HTTPS is recommended across untrusted public
networks. mTLS remains a future hardening layer and is not required for
alpha.8 operation.

## Network listeners

The control-plane binary owns two listeners. The panel UI and administrator API
use `panel_port` (`8080` by default), while desired-state and heartbeat traffic
use `agent_port` (`8081` by default). Both values live in SQLite so the
unprivileged panel can change them without writing `/etc`. Saving network
settings schedules a controlled process exit; systemd restarts the binary and
the browser moves to the new panel port.

For migration, the panel listener continues accepting the alpha.7 agent paths.
After a port change, the immediately previous panel and agent ports can remain
as agent-only listeners; this bounded pair covers nodes enrolled by both old and
new releases without exposing another administrator UI. Temporary node
disablement blocks credential validation without deleting the credential.

## Telemetry and decommission

Beta.1 heartbeat payloads contain a one-minute in-memory aggregate for CPU,
RAM, load average, host network throughput and unique source addresses seen in
the IPVS connection table. The panel stores only the latest aggregate, so the
database does not grow as a time-series store.

Remote-node removal is acknowledged. The panel first marks the node as
`deleting`; after reconnecting, the agent removes only EzhikLB-managed IPVS
services and firewall chains, reports completion, disables its systemd unit and
exits. The credential and node row are removed only after this acknowledgement.

## Autonomous restore and profile versions

The node agent stores only its last successfully applied desired state. At startup it restores the corresponding IPVS services, destinations and managed firewall rules before attempting control-plane reconciliation. A panel outage therefore prevents new configuration from arriving but does not intentionally remove or pause the last known data plane.

SQLite keeps an internal monotonic revision number for reconciliation while operators see a separate immutable profile version label. Automatic mode derives `vN` from the next revision; manual mode accepts only ASCII letters, digits, dots and hyphens and requires a new label for every publication.

An optional profile publication can attach a one-shot connection reset to the new revision.
The store records that revision only on nodes already assigned to the profile; a successful
heartbeat acknowledging the revision clears the marker. A `1.0.7+` agent handles it by deleting
and rebuilding only EzhikLB-owned IPVS services and deleting conntrack entries filtered by the
affected protocol, VIP and port. It never invokes host-wide `ipvsadm -C` or `conntrack -F`.
Publication is rejected when any assigned agent is older than `1.0.7`, preventing an older
JSON client from silently ignoring the reset request.

Audit events are operational history rather than telemetry. They are pruned to a rolling 14-day window during writes and reads; node resource metrics continue to store only the latest aggregate.

## Node self-update and diagnostics

Beta.3 adds a one-minute node-metric history (`node_metric_history`, pruned to a rolling 24-hour
window) that backs the Overview charts, and a read-only diagnostics probe
(`agent.CollectDiagnostics`) reporting whether `ipvsadm` responds and whether the
`EZHIKLB-FORWARD`/`EZHIKLB-SNAT` chains exist, alongside current service/destination counts.

Node self-update keeps the existing security boundary intact: the panel never sends a shell
command, only a target version string (`NodeDesiredState.UpdateVersion`, taken from the panel's
own build version). The agent is solely responsible for turning that into an update: it fetches
the matching official `ezhiklb_<version>_linux_amd64.tar.gz` release asset and its `.sha256`
file from GitHub Releases, verifies the checksum before touching anything on disk, extracts only
the `ezhiklb-agent` binary, and atomically renames it over the running executable before asking
systemd to restart the service. A failed download, checksum mismatch or extraction error leaves
the running binary untouched and is reported back as `update_state=error` with the failure
reason, never partially applied.

The agent reports each stage (`requested`, `downloading`, `verifying`, `installing`,
`restarting`) via an immediate heartbeat as that stage starts, rather than sending a single
opaque "updating" state for the whole operation. The panel stores whatever `update_state` string
the agent sends verbatim (no server-side enum) and only overrides it to `completed` once a
heartbeat arrives whose reported `version` matches the requested `update_target`. This keeps the
progress reporting honest — it reflects what the agent is actually doing, not a fabricated
timer — while remaining forward-compatible with future stage names without a schema change.

Self-update is inherently bootstrap-limited: a node can only react to a panel-issued update
request if its *currently running* agent binary already contains this polling/reporting logic.
A node still on a pre-`beta.3` agent must be upgraded once through the ordinary install command
before the one-click button has any effect on it.

The agent service keeps `ProtectSystem=strict`; its `ReadWritePaths` allow only the agent state
directory and `/opt/ezhiklb/bin`. The latter is required for the verified temporary binary and
atomic rename used by self-update, while the remainder of the system image stays read-only in
the service mount namespace.
