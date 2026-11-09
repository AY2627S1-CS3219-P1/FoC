# kind + systemd-resolved host: CoreDNS upstream dead, ndots search path stalls probes

**Applies when:** bootstrapping the local kind cluster on a host whose
`/etc/resolv.conf` points at `127.0.0.53` (systemd-resolved) with a search
domain (e.g. `nus.edu.sg`), or whenever cluster DNS lookups take >5 s per name
while the kind node itself works.

**Symptom:** a dependency pod never becomes Ready although its process is fully
started and healthy. In this incident Kafka's readiness probe
(`kafka-topics.sh --bootstrap-server 127.0.0.1:9092 --list`) failed forever:
the client connects, fetches metadata, then must re-resolve the **advertised**
listener (`kafka.foc.svc.cluster.local`) — and with `ndots:5` the resolver first
tries `kafka.foc.svc.cluster.local.<host-search-domain>`, which CoreDNS
forwards upstream to `172.18.0.1:53` (the docker bridge gateway) where **no
DNS server listens** (`i/o timeout` per query, ~5 s each). The probe's 10 s
client budget dies inside that window, 40 probe failures kill the container,
and the StatefulSet crash-loops. `kubectl exec`-based diagnostics that "work"
(a successful metadata response over direct loopback) mislead — the failure is
only visible on the second, advertised-listener connection.

**How to proceed:**

1. Confirm with `kubectl -n kube-system logs -l k8s-app=kube-dns | grep
   "i/o timeout"` — repeated `read udp ...->172.18.0.1:53: i/o timeout` entries
   are this landmine.
2. Fix the cluster, not the host: repoint CoreDNS's forward at a working
   resolver (`forward . 8.8.8.8 1.1.1.1` in the `coredns` ConfigMap; CoreDNS
   reloads automatically within ~30 s). Verify from a pod:
   `time getent hosts <service>.<ns>.svc.cluster.local` must return in <1 s.
3. A StatefulSet whose pod never goes Ready will not roll out a patched pod
   template — after fixing DNS, `kubectl delete pod <pod>` to force recreation
   with the new template.
4. Do not "fix" this by lengthening the probe timeout: with the upstream dead,
   resolution takes 5–10 s per name and every client with a tight
   `socket.connection.setup.timeout.ms` still fails.

**Remove this entry when:** the kind bring-up installs a CoreDNS forward that
does not depend on the host's docker-bridge gateway (or the host resolves
without systemd-resolved), so upstream timeouts cannot recur.
