# Test-node checklist

The workspace intentionally does not build or run EzhikLB. Push the repository
to GitHub and let the release workflow create the Linux bundle.

## Install

1. Create tag `v0.1.0-alpha.5` and download the generated
   `ezhiklb_0.1.0-alpha.5_linux_amd64.tar.gz` asset on a test node.
2. Verify the adjacent SHA-256 file.
3. Extract the archive and run `sudo ./install.sh`.
4. Select `Panel + Node`.
5. Open an SSH tunnel printed by the installer and use the generated token.

The panel listens on loopback in this alpha. Do not expose plain HTTP directly
to the internet. A later installer iteration will configure a domain and HTTPS.

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
