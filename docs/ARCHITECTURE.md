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
alpha.7.3 operation.
