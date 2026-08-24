# Test-node checklist

The workspace intentionally does not build or run EzhikLB. Push the repository
to GitHub and let the release workflow create the Linux bundle.

## Install

1. Create tag `v0.1.0-alpha.7.2` and download the generated
   `ezhiklb_0.1.0-alpha.7.2_linux_amd64.tar.gz` asset on a test node.
2. Verify the adjacent SHA-256 file.
3. Extract the archive and run `sudo ./install.sh`.
4. Select `Panel + Node`.
5. Select local access for an SSH-only test or network access when a remote node must reach the panel.
6. Open the address printed by the installer and use the generated token.

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
## Alpha.7 acceptance checks

Run these only on disposable test VPS nodes.

1. Upgrade the panel-node and confirm the existing database/configuration are preserved.
2. Open a UDP VPN rule, select affinity `5 часов`, publish, and verify `ipvsadm -Ln` shows persistence `18000`.
3. Lock the phone for at least 6 minutes, then verify Telegram/browser traffic resumes without reconnecting the VPN.
4. Add a second backend with equal weight and verify an approximate 50/50 distribution; change weights to 2/1 and verify an approximate 66/33 distribution over many independent clients/flows.
5. Enable ICMP health-check, make one backend unreachable, wait for the configured failure threshold and verify its effective IPVS weight becomes zero. Restore it and verify recovery after the success threshold.
6. Create a TCP-only rule and a TCP+UDP rule on unused ports; verify both protocols forward correctly.
7. Create a remote node by entering only its name, copy the generated one-line command to a second disposable VPS, and verify version, last seen and applied/desired revisions appear in Nodes.
8. Repeat enrollment once over HTTP and verify the generated command includes `EZHIKLB_ALLOW_INSECURE=1`; repeat over HTTPS without that flag.
9. Assign the same profile to both nodes, publish a change, and verify both converge to the same revision.
10. Rotate the remote-node key and verify the old agent is rejected until its environment is updated.
11. Clone a profile, inspect revision history, roll back an older revision and verify rollback creates a new revision instead of deleting history.
