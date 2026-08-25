# Changelog

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
