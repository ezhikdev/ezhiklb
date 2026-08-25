# Changelog

## 0.1.0-beta.3

- Added minute-bucket node metric history with a 24-hour retention window and four Overview
  charts (network, CPU, RAM, active IPs) with a per-node/all-nodes selector.
- Added a node diagnostics check (IPVS reachability, EzhikLB firewall chain readiness, service
  and destination counts) shown on every node's detail dialog.
- Added a signed one-click node self-update: the panel only ever hands the agent a target
  version, the agent downloads the matching official release archive, verifies its SHA-256
  checksum and atomically replaces its own binary before restarting.
- Replaced the "online" text badge next to each node with status squares: pulsing green
  (online), blinking orange (applying), pulsing red (offline/unreachable), static dark
  (disabled).
- Replaced the remaining browser `confirm()` prompts (closing an unsaved profile or listener,
  restoring a profile revision, deleting a routing entry) with the panel's own confirmation
  dialogs.
- Translated the Health page's reachability states and latency label into Russian.
- Improved the event journal: added labels for token rotation/revocation, manual health probes
  and update requests, and resolved node/profile IDs to their current names when the audit
  record itself has no name.
- Added a non-blocking warning when a listener's backend address matches one of the panel's
  known node IPs.
- Aligned the local node's two action buttons to the same optical positions a third button would
  use, matching the row width of remote nodes.

## 0.1.0-beta.2

- Added autonomous restoration of the last successfully applied IPVS and firewall state when a node boots while the panel is unavailable.
- Added automatic `v1`, `v2`, `v3` profile versions and validated custom versions with immutable publication history.
- Replaced internal revision counters in the node interface with clear apply status and semantic profile version labels.
- Added an event journal with node, profile and error filters and automatic 14-day retention.
- Added the project GitHub link to the sidebar and aligned compact load metrics with the rest of each node row.

## 0.1.0-beta.1

- Added lightweight one-minute node telemetry for RAM, CPU, load average, network throughput and unique active client IPs.
- Added compact live node cards with resource meters, traffic rates, connection uptime and an accessible online pulse.
- Changed remote-node deletion into an acknowledged decommission flow that removes managed IPVS/firewall state and disables the agent before the panel forgets the node.
- Added a persistent `deleting` state for offline nodes so cleanup resumes when they reconnect.
- Allowed ordinary upgrades of already enrolled nodes while the panel is temporarily unavailable; strict first-revision verification remains enabled for new and replacement enrollment.
- Changed the agent unit to restart only on failure so a completed decommission can stop it cleanly.

## 0.1.0-alpha.8.2

- Fixed reinstalling or re-enrolling an existing node VPS so the supplied panel URL, node ID and credential replace stale values in the persisted environment.
- Fixed ordinary node upgrades so saved enrollment values are reused without asking for the ID and credential again.
- Increased the installation-command viewport and separated its text from the horizontal scrollbar.
- Replaced the enrollment radar with a clear animated panel-to-node connection path and simplified the waiting copy.

## 0.1.0-alpha.8

- Split the administrator panel and node-agent API onto configurable ports, defaulting to `8080` and `8081`.
- Added safe in-panel port changes with automatic service restart, browser redirect and bounded previous panel/API listeners for migration.
- Rebuilt node enrollment around a single-line scrollable command, copy action and live connecting/success animation.
- Added automatically observed node IPv4 addresses, continuous online uptime, last heartbeat and apply-stage reporting.
- Simplified node actions to edit, enable/disable and delete while retaining credentials during temporary disablement.
- Added node detail dialogs with profile, revision, health, error and live IPVS route information.
- Grouped dashboard traffic by expandable node sections.

## 0.1.0-alpha.7.3

- Centered the animated checkmark precisely inside every square state control.
- Replaced manual backend action offsets with stable vertical grid alignment.

## 0.1.0-alpha.7.2

- Replaced unstable slider switches with accessible square checkboxes and animated SVG checkmarks across listeners, backends and health checks.

## 0.1.0-alpha.7.1

- Fixed all dialogs being positioned relative to the animated page container, which cropped node and listener editors.
- Print the detected server IPv4 after network-access panel installation instead of `0.0.0.0` and a placeholder.

## 0.1.0-alpha.7

- Simplified the public README to one interactive installation command and a focused two-VPS test flow.
- Added interactive panel access and node enrollment prompts to the installer.
- Reduced new-node creation to a name-first two-step flow with automatic initial profile assignment.
- Hardened the generated one-line node command with dependency installation and SHA-256 verification.
- Added automatic `EZHIKLB_ALLOW_INSECURE=1` enrollment when the selected panel URL uses HTTP.
- Marked alpha tags as GitHub pre-releases automatically.
- Replaced the oversized Affinity panel with a compact custom dropdown and optional custom-seconds field.
- Replaced native profile/scheduler selects with the shared EzhikLB dropdown component.
- Replaced browser prompts for node editing and profile cloning with proper panel dialogs.
- Fixed backend switch alignment and constrained dialogs to the viewport without horizontal overflow.
- Increased small labels, metadata, helpers, table values and navigation text throughout the panel.

## 0.1.0-alpha.6

- Fixed the rule-editor field grid and increased small text throughout the panel.
- Replaced the raw affinity number with documented presets: off, 15/30 minutes, 1/3/5/24 hours, and a custom seconds value.
- Added guidance for stateful UDP/VPN traffic and backend failover behavior.
- Added profile cloning, guarded deletion, revision history and rollback-as-a-new-revision.
- Added remote-node creation with a unique credential shown once, generated install command, credential rotation, rename and removal.
- Added remote-node disable/re-enable and on-demand ICMP probes executed by each node agent.
- Kept reusable profile assignment for both local and remote nodes and expanded node version/last-seen/apply status.
- Remote agents now reject public plain HTTP unless the test-only `EZHIKLB_ALLOW_INSECURE=1` switch is explicit.
- Added an alpha.6 multi-node, TCP/UDP, weighted distribution and health-failover test plan.

## 0.1.0-alpha.5

- Replaced expanded listener cards with compact Cloudflare-style rule rows.
- Added focused rule editing, cloning, delete confirmation and unsaved-change protection.
- Added client/server validation for conflicting listeners and duplicate backends.
- Added calculated backend weight percentages and inline ICMP health state.
- Added persisted per-service and per-backend IPVS counters to the Overview page.
- Added explicit applying/applied/error node states.
- Reduced heartbeat write frequency to 15 seconds and added revision ETags.
- Improved upgrade backups and rollback data restoration.
- Added safe cache headers so panel upgrades load the current frontend.

## 0.1.0-alpha.4

- Fixed listener creation on panels opened over plain HTTP.

## 0.1.0-alpha.3

- Fixed executable directory permissions and installer version detection.
