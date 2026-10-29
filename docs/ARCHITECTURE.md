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

Since 1.2.1 the panel requires a unicast IPv4 when creating or editing a node.
The configured ingress takes precedence over the observed connection address
in the UI. Desired-state ETags include the ingress address, and the agent
reconciles when either the profile revision or ingress changes. Wildcard
listeners use this address; explicitly addressed listeners retain their IP.

Local uninstall uses `ezhiklb-agent --uninstall` after the script stops the
agent service. For remote nodes, authenticated `POST
/agent/v1/nodes/{id}/decommission` first marks the node as deleting through
the existing store method. Only a successful cleanup heartbeat then removes
the record. The normal deletion acknowledgement guard remains unchanged.
Cleanup failures preserve on-disk ownership state; panel connection failures
allow local uninstall with a warning to remove the record manually.

## Data plane

Each enabled UDP listener becomes one IPVS virtual service. Backends keep IPVS
NAT mode and may have a destination port different from the listen port. TCP
listeners are rendered into an isolated HAProxy layer-4 configuration managed
by `ezhiklb-haproxy.service`; the distribution package's own
`haproxy.service` and `/etc/haproxy/haproxy.cfg` are never modified.
Since 2.0.2 the generated config has no per-connection log (VPN clients open
hundreds of connections per second; the log chain cost CPU and disk), an
unchanged config is not reloaded (runtime weights are re-synced over the admin
socket instead, so no extra worker generation is left draining), and active
client IPs are read from the kernel with `ss` instead of HAProxy `show sess`. The agent
owns only IPVS services recorded in its state file and never calls
`ipvsadm -C`.

An optional listener bandwidth limit is applied independently by each node.
The agent marks packets by IPVS virtual service in its own mangle chain, then
uses two `clsact` egress policers: one for original client-to-backend traffic
and one for reply backend-to-client traffic. This limiter applies only to
UDP/IPVS; TCP/HAProxy is not placed in the IPVS-aware mangle chain. The two
directions receive the same configured Mbit/s value but do not consume one
another's budget. EzhikLB never replaces the interface root qdisc.
When no current or previously persisted listener limit exists, reconciliation
does not issue any mutating `tc` or rate-limit mangle command.

Reconciliation is differential:

1. Validate the complete desired revision.
2. Resolve routes and SNAT source addresses.
3. Prepare EzhikLB-owned firewall chains.
4. Add or update virtual services and destinations.
5. Quiesce and remove obsolete destinations.
6. Remove obsolete virtual services.
7. Persist the applied state and report the actual revision.

### Hidden profiles (2.0)

A profile with `hidden: true` has TCP records identified by `sni` instead of a
listen socket. The agent compiles each enabled record into a TCP service with
the node's ingress address, port 443 (a statistics label) and the normalised
SNI, so service keys stay unique. HAProxy renders one shared frontend:

```text
fe_ezhiklb.sni  bind INGRESS:80, INGRESS:443
  tcp-request inspect-delay 5s
  accept once the full ClientHello (req.ssl_sni) or any non-TLS byte arrived
    (with HTTP records: or complete HTTP request headers)
  use_backend be_<record> if SNI matches   exact names first, then *.suffix
  use_backend be_<record> if HTTP Host/path match   (2.0.3, see below)
  TLS without a match  -> be_ezhiklb.reject-tls   -> abns strict-sni listener (TLS alert)
  anything else        -> be_ezhiklb.reject-plain -> abns HTTP listener (400, silent close when idle)
```

The strict-sni listener needs a certificate to parse; the agent creates a
self-signed placeholder with a random `.invalid` name (`sni-reject.pem` next to
`state.json`) that no client ever requests. Record backends are the usual
`be_<listener>` proxies, so weights, affinity, health weight changes and
statistics work unchanged; statistics carry the SNI.

Since 2.0.3 a record with `match_host: true` is routed by plain HTTP instead:
`sni` is compared with the Host header (port stripped; empty matches any host)
and `path` with the request path (`/ws` renders `{ path /ws }` and
`{ path_beg /ws/ }`; empty or `/` matches any path). HAProxy evaluates these
HTTP fetches on the raw buffer of the TCP frontend after
`tcp-request content accept if HTTP` has waited for the complete headers, so
the connection is still spliced through unchanged (WebSocket, HTTPUpgrade,
xhttp) and only its first request selects the record. Order: exact Host,
wildcard (longest first), any Host; longest path first within a Host. TLS
hides the path, so a TLS connection only ever matches SNI records and an SNI
record cannot have a path. HTTP records do not redirect to https. Their
statistics key is `domain.RouteLabel` (`http://host/path`, `*` for any host).
The panel refuses to publish or assign such a profile to agents older than
`HTTPRouteMinAgentVersion` (2.0.3); SNI-only profiles render exactly as in
2.0.2.

When a hidden revision is applied the agent installs `EZHIKLB-INPUT` as the
first `INPUT` rule for IPv4 and IPv6. It RETURNs loopback, ESTABLISHED/RELATED,
ICMP/ICMPv6, DHCP replies and TCP 22/80/443 plus the ports of listening
`sshd`, `dropbear` and `ezhiklb` (panel) processes, and DROPs everything else.
RETURN hands accepted traffic back to the host's own rules, so the chain only
narrows host policy. `AppliedState.Hidden/InputPorts` persist it: `Restore`
re-applies it at boot with the saved ports plus fresh detection, and the
minute diagnostics loop (`EnsureInputFirewall`) follows port changes and
repairs a removed jump. Leaving hidden mode and decommission remove the chain;
regular nodes never run inbound-firewall commands. Docker ports published via
DNAT traverse FORWARD and are outside this chain.

The panel refuses to publish or assign a hidden profile to nodes with an
agent older than 2.0.0 and the agent API answers 409 to such agents, which
would otherwise apply the records as ordinary 443 listeners.

Since 2.0.5 a hidden record whose `protocols` is exactly `["udp"]`
(`domain.HiddenUDP`) is a plain UDP record: `listen_port` must be one of
`domain.HiddenPorts` (80 or 443), each port has at most one UDP record, and
`sni`/`match_host`/`path` are ignored (kept so switching the record back to
TCP loses nothing). `compileServices` turns it into an ordinary IPVS UDP
service on `INGRESS:port` without an SNI label, so the existing UDP path
(firewall, SNAT, weights, affinity, health, statistics) applies unchanged and
HAProxy never sees it; TCP on the same port stays with `fe_ezhiklb.sni`.
The filter `INPUT` hook runs before IPVS takes a packet, so `EZHIKLB-INPUT`
gets `-p udp -m multiport --dports <ports> -j RETURN` for the ports of the
state's UDP services (`hiddenUDPPorts`), IPv4 only because IPVS serves the
IPv4 ingress address. The ports are derived from `AppliedState.Services`, not
persisted separately; `inputFirewallActive` also requires the UDP rule, so the
minute `EnsureInputFirewall` re-applies a chain that lost it, and diagnostics
report `allowed_input_udp_ports`. Publishing, assigning and the agent API are
gated on `HiddenUDPMinAgentVersion` (2.0.5) via `ProfileConfig.HasHiddenUDP`.

### Desired-state identity

Revision numbers are unique only within a profile. Since 2.0 the desired-state
ETag, the agent's applied state and heartbeats carry the profile ID as well,
so switching a node to another profile with the same revision number is
applied. Agents 1.x keep receiving their old ETag format.

Source affinity is optional and defaults to zero. IPVS already keeps packets
within an existing flow on the same destination; affinity adds a broader
source-IP template and changes how weights are observed.

## Panel sessions (2.0)

The login exchanges the administrator token for an HMAC-signed session cookie
(`v2.<expiry+nonce>.<mac>`, 12 hours). The key is derived from the token, so
sessions survive panel restarts and die with a token change; logout revokes
the nonce in memory. Failed logins are limited to 10 per client address in 10
minutes. Mutating admin requests must be JSON and not cross-site
(`Sec-Fetch-Site`), which blocks CSRF including other sites on the same host.

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

### Receive steering (2.0.2)

`configureKernel` also calls `tuneReceiveSteering` (`internal/agent/rps.go`).
A single-queue VPS NIC raises one RX interrupt pinned to one CPU, and with the
kernel default (RPS off) all softirq packet processing for IPVS and every
HAProxy session lands on that CPU. For each interface with fewer RX queues
than online CPUs (not `lo`, not a bridge) whose `rps_cpus` mask is still empty,
the agent writes the all-CPU mask, sets `rps_flow_cnt` and raises
`net.core.rps_sock_flow_entries` to 65536 (RFS). Administrator masks and larger
values are kept; failures (read-only sysfs in containers) are only logged.

### TCP buffer memory (2.0.4)

`internal/agent/tcpmem.go` manages `net.ipv4.tcp_mem`. The kernel default is
about 9% of RAM; a proxy holding two sockets per client exhausts it, the kernel
then shrinks windows and prunes received packets (`OfoPruned`, `TCPRcvQDrop`),
and throughput collapses with an idle CPU and no NIC/softnet/qdisc drops.

The agent's minute diagnostics call `TCPMemoryTuner.Tick`:

- re-read `MemTotal`/`MemAvailable`, online CPUs, `tcp_mem`, TCP pages in
  `/proc/net/sockstat`, `TcpExt TCPMemoryPressures`, `oom_kill` and memory
  PSI. A changed vCPU count calls `Reconciler.RetuneReceiveSteering`, which
  rewrites RPS masks still holding the mask the agent wrote before;
- target hard limit = (percent + adaptive) % of RAM, capped at RAM minus a
  reserve of max(512 MiB, 15% RAM); low and pressure are 5/7 and 6/7 of it
  (50/60/70% for the default 70%);
- adaptive step +5% when pressure events grew since the last check, usage is
  at >= 90% of the pressure threshold, the step fits under the cap, no OOM
  kill happened, PSI `some avg10` < 10 and `MemAvailable` covers the reserve
  plus the growth;
- raises are written at once. Lowering (a smaller percentage, less RAM) waits
  until usage is below the new low threshold, and a current value that the
  agent did not write is never lowered;
- events (deduplicated for 10 minutes) go to the journal and to the last 20
  entries in `NodeDiagnostics.TCPMemory`.

The setting (`nodes.tcp_tuning_enabled`, `nodes.tcp_memory_percent`, default
on/70) travels in `NodeDesiredState.TCPTuning` and in the desired-state ETag
(`...-tcp-<enabled>-<percent>`) for agents >= 2.0.4; older agents get the
2.0.3 tag. The agent persists it in `tcp-tuning.json` next to `state.json`.
Inside a container network namespace `tcp_mem` does not exist; the status
says so and nothing is changed.

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
