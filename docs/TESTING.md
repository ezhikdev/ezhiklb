# Test-node checklist

The workspace intentionally does not build or run EzhikLB. Push the repository
to GitHub and let the release workflow create the Linux bundle.

## Install

1. Create tag `v1.0.2` and download the generated
   `ezhiklb_1.0.2_linux_amd64.tar.gz` asset on a test node.
2. Verify the adjacent SHA-256 file.
3. Extract the archive and run `sudo ./install.sh`.
4. Select `Panel + Node`.
5. Select local or network access for the administrator web interface. The node API uses its own network listener in both modes.
6. Accept the default panel/API ports (`8080`/`8081`) or enter two different free ports.
7. Verify that a non-numeric, out-of-range, duplicate or already occupied port is rejected with a clear prompt.
8. Open the address printed by the installer and use the generated token.

Repeat the installation with the `Panel` role on a clean database and verify that
the node list is empty. Updating a panel-only `1.0.1` installation must remove its
phantom `Local node` while leaving every enrolled remote node intact.

The default web port is `8080`; the dedicated node API is `8081`. Allow the
agent port in the VPS firewall before enrolling a remote node.
An upgrade must preserve the ports already stored in `/etc/ezhiklb/ezhiklb.env`
without asking for them again.

The installer can bind the panel to loopback or all network interfaces. HTTP is
supported when the generated node command explicitly enables insecure mode;
prefer HTTPS or a private network when credentials cross an untrusted network.

## Verify control plane

```bash
sudo systemctl status ezhiklb ezhiklb-agent --no-pager
sudo journalctl -u ezhiklb -u ezhiklb-agent -n 100 --no-pager
sudo cat /var/lib/ezhiklb-agent/state.json
sudo ipvsadm -Ln
sudo ipvsadm -Ln --stats --exact
sudo iptables -S EZHIKLB-FORWARD
sudo iptables -t nat -S EZHIKLB-SNAT
```

## First-packet reproduction

Create a UDP listener in the panel, publish its profile, and wait until the node
shows matching desired and actual revisions. Then stop client traffic for ten
minutes. Immediately before resuming traffic run:

```bash
sudo ipvsadm -Lnc
sudo conntrack -L -p udp -o extended 2>/dev/null
sudo ip neigh show
sudo tcpdump -ni any 'udp port 8002' -tttt -vv
```

After the first client attempts collect:

```bash
sudo ipvsadm -Ln --stats --rate
sudo ipvsadm -Lnc
sudo conntrack -L -p udp -o extended 2>/dev/null
sudo journalctl -u ezhiklb-agent --since '-15 minutes' --no-pager
sudo sysctl \
  net.netfilter.nf_conntrack_udp_timeout \
  net.netfilter.nf_conntrack_udp_timeout_stream \
  net.ipv4.vs.expire_nodest_conn \
  net.ipv4.vs.expire_quiescent_template
```

Remove or mask client IP addresses before sharing logs. Keep timestamps, ports,
packet directions, IPVS states and backend addresses intact.

The Overview page should show the same service/backend counters within one
15-second heartbeat interval.

## Rollback

Installer backups are stored in `/var/backups/ezhiklb/<timestamp>`. The legacy
`/etc/ezhik-udp/ezhik-udp.conf` file is never modified. If the first agent apply
fails during migration, the installer stops the new agent and starts the old
`ezhik-udp.service` again when it had been active before installation.
## Beta.2 acceptance checks (still applicable)

Run these only on disposable test VPS nodes.

1. Upgrade the panel-node and confirm the existing database/configuration are preserved.
2. Open a UDP VPN rule, select affinity `5 часов`, publish, and verify `ipvsadm -Ln` shows persistence `18000`.
3. Lock the phone for at least 6 minutes, then verify Telegram/browser traffic resumes without reconnecting the VPN.
4. Add a second backend with equal weight and verify an approximate 50/50 distribution; change weights to 2/1 and verify an approximate 66/33 distribution over many independent clients/flows.
5. Enable ICMP health-check, make one backend unreachable, wait for the configured failure threshold and verify its effective IPVS weight becomes zero. Restore it and verify recovery after the success threshold.
6. Create a TCP-only rule and a TCP+UDP rule on unused ports; verify both protocols forward correctly.
7. Create a remote node by entering only its name, copy the generated one-line command to a second disposable VPS, and verify the waiting animation changes to success without refreshing the page.
8. Repeat enrollment once over HTTP and verify the generated command includes `EZHIKLB_ALLOW_INSECURE=1`; repeat over HTTPS without that flag.
9. Assign the same profile to both nodes, publish a change, and verify both converge to the same revision.
10. Disable and re-enable the remote node; verify the same installed agent reconnects without a new key or reinstall command.
11. Clone a profile, inspect revision history, roll back an older revision and verify rollback creates a new revision instead of deleting history.
12. Change the panel and agent ports in Settings, verify the browser redirects after restart, new enrollment commands use the new agent port and both immediately previous ports still accept existing node agents.
13. Verify automatic IPv4, connection uptime, last heartbeat, apply stage, node details and expandable dashboard routes.
14. On a VPS with an older node installation, run a newly generated enrollment command and verify `/etc/ezhiklb/ezhiklb.env` receives the new node ID, API URL and credential before the agent starts.
15. Stop the panel, run the ordinary beta.2 upgrade on an already enrolled node and verify installation succeeds while the agent keeps retrying in the background. Start the panel and verify the node returns online without re-enrollment.
16. Keep traffic active for at least two heartbeat intervals and verify the node card reports RAM, one-minute CPU/load, receive/transmit rates and unique active IPs without continuously growing the SQLite database.
17. Delete a remote node while it is online. Verify its managed IPVS services and `EZHIKLB-FORWARD`/`EZHIKLB-SNAT` chains disappear, `ezhiklb-agent.service` becomes disabled and the row disappears from the panel only after acknowledgement. Repeat while the node is offline and verify the `deleting` state completes after reconnecting it.
18. Publish a profile automatically twice and verify the versions advance to `v2` and `v3`. Switch to manual mode, verify invalid characters are rejected and verify an unchanged version cannot be published.
19. Stop the panel, reboot a node and verify the saved IPVS services and firewall state return before panel connectivity is restored. Start the panel and verify the node converges without reinstalling.
20. Trigger profile and node actions, open Journal and verify all/node/profile/error filters. Confirm events older than 14 days are removed.

## Beta.3 acceptance checks

21. Install an older release on a node, then in the panel click its "Обновить до <version>" button, confirm the dialog, and verify the agent downloads the matching release, restarts and reports the new version — with a green toast on success. Point the node at a corrupted/mismatched release (or block the download) and verify it reports `update_state=error` with a red toast instead of leaving the agent half-updated.
22. Open a node's detail dialog and verify the diagnostics card reports IPVS/firewall readiness and service/destination counts that match `sudo ipvsadm -Ln` and `sudo iptables -S EZHIKLB-FORWARD` on that VPS.
23. On Overview, switch the chart selector between "Все ноды" and an individual node and verify all four charts (network, CPU, RAM, active IPs) update; confirm the charts remain non-empty after 24+ hours of continuous heartbeats (history should not grow past the retention window).
24. Add a backend whose address equals a node's own IP/observed address and verify the profile editor shows a non-blocking warning without preventing publish.
25. Close a profile or listener editor with unsaved changes, delete a routing entry and roll back a profile revision, and confirm every one of these now uses the panel's own confirmation dialog (no browser-native confirm popup).
26. Compare a local node's row against a remote node's row and verify both action-button groups line up at the same horizontal positions.

## Beta.3.1 acceptance checks

27. Ensure the target node's currently installed agent is `beta.3` or newer (reinstall once via the classic command if it predates the self-update trigger), then click "Обновить" and verify an orange progress bar appears under the node row and visibly advances through downloading/verifying/installing/restarting stages (cross-check timestamps against `sudo journalctl -u ezhiklb-agent`). Verify the update button itself is hidden while the bar is visible.
28. Verify the completion toast (green) or failure toast (red) appears only once the node's own reported state actually reaches `completed`/`error` — not immediately after clicking the confirm dialog.
29. Hover over each of the four Overview charts and verify a tooltip follows the pointer showing the exact time and per-series value, with a crosshair and per-series dot on the line; verify it also works via a single tap on a touch device.
