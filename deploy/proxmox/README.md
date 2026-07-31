# Proxmox host tooling

Runs on the **Proxmox host**, not on Delta and not in a container.

## Why the IP-conflict watch exists

`10.248.40.154` (DB1) and `10.248.40.183` (MaxScale) were taken from Drexel's
DHCP pool by one-off `dhclient` runs. They are now static in **both** the
container config (`pct config <id>` → `net0 ... ip=`) and inside the container
(`/etc/systemd/network/10-eth0-static.network`), so our hosts keep their
addresses — that part is solved, and it is what fixed DB1's ~20-minute outage
when its lease lapsed on 2026-07-29.

What is **not** solved: those addresses were never excluded from the pool.
DHCP for this subnet is central Drexel (`dhcp-server-identifier 10.254.5.41`,
off-subnet via a relay), so **nothing on the Proxmox host can reserve or exclude
an address** — that needs a request to Drexel IT, and as of 2026-07-30 none has
been made. The old leases expire **2026-08-05**, after which that server may
hand `.154` or `.183` to another device.

Two hosts answering for one address does not fail cleanly. It looks like
intermittent, unexplainable connection errors against the database. This check
turns that into an immediate, named alert.

**It detects; it does not prevent.** The fix is still a pool exclusion from
Drexel IT.

## Install

```bash
install -m 0755 ip-conflict-watch.sh /usr/local/sbin/ip-conflict-watch.sh
install -m 0644 ip-conflict-watch.service /etc/systemd/system/
install -m 0644 ip-conflict-watch.timer   /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now ip-conflict-watch.timer
```

Verify, and confirm it reports the expected MACs:

```bash
systemctl start ip-conflict-watch.service
journalctl -t triangle-ip-watch -n 10 --no-pager
systemctl list-timers ip-conflict-watch.timer
```

Expected output:

```
[daemon.info] 10.248.40.154 OK (bc:24:11:e5:cc:58)
[daemon.info] 10.248.40.183 OK (bc:24:11:83:05:ea)
```

## Exit codes

| code | meaning |
| --- | --- |
| 0 | both addresses answered, only from the expected MAC |
| 1 | **conflict** — a foreign MAC also answered |
| 2 | a host did not answer ARP at all (container down, or wrong `IFACE`) |

A non-zero exit fails the unit, so `systemctl status ip-conflict-watch` and
`systemctl list-units --failed` both surface it.

## Configuration

Defaults are in the script. To change the interface or watch more hosts without
editing it, drop `/etc/triangle-ip-watch.conf`:

```bash
IFACE=vmbr0
WATCH=(
  "10.248.40.154=bc:24:11:e5:cc:58"
  "10.248.40.183=bc:24:11:83:05:ea"
)
```

Keep the MACs in step with `pct config <id> | grep net0`. A stale expected MAC
produces a false conflict alert.

## Getting alerted

The check logs to the journal under tag `triangle-ip-watch`, at `daemon.err`
for a conflict. That is deliberately the whole of it — how alerts leave this
box is a local decision. Two options:

- **systemd**, mail on failure — add `OnFailure=status-email@%n.service` to the
  service unit with a mail-sending template unit.
- **Promtail**, if the observability stack is ever pointed at this host — scrape
  the journal and alert on the `triangle-ip-watch` tag at priority 3.

## After 2026-08-05

Re-check by hand once the old leases have lapsed, since that is the window when
a reassignment can first happen:

```bash
arping -I vmbr0 -c 2 10.248.40.154   # expect bc:24:11:e5:cc:58
arping -I vmbr0 -c 2 10.248.40.183   # expect bc:24:11:83:05:ea
```

Note `arping -D` from the Proxmox host **always** gets a reply — our own
containers answer their own probes. That is not a conflict. Identity needs plain
`arping` (which prints the responder MAC) or `ip neigh show <ip>`.

## Still to do

- Request a **pool exclusion** (not a reservation — these hosts never request a
  lease, so a reservation would never be exercised) for `.154` and `.183` from
  Drexel IT, and ask them to confirm the dynamic range so it can be verified.
- CT 113 `THETRIANGLE-REACT` is still `ip=dhcp`. It *does* request a lease, so
  for that one a **reservation** is the right ask.
