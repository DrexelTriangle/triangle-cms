# Production MariaDB — primary + MaxScale (native installs, off Delta)

The database tier runs on its own hosts, **not** on Delta and **not** in Docker.
The CMS opens a single connection to the endpoint configured as
`DB_HOST`/`DB_PORT` in Delta's host-only `cms.env`; that endpoint is MaxScale,
which splits writes to the primary and reads to the replica.

```
 CMS / app host (Delta)            DB primary host        DB replica host
 ┌───────────────────────┐         ┌────────────────┐     ┌────────────────┐
 │ cms-blue   cms-green  │  writes │ DB1            │ GTID│ DB2 (planned)  │
 │      │        │       │────────▶│  (server_id 1) │────▶│  (server_id 2) │
 │      └────┬───┘       │         │  binlog+ACID   │async│  read_only     │
 └───────────┼───────────┘  reads  └────────────────┘     └────────────────┘
             └──────────▶ MaxScale (rwsplit :4006) ──────────────▶
```

| Host | Address | Role | Software |
| --- | --- | --- | --- |
| `THETRIANGLE-DB1-LXC` | `10.248.40.154` | primary | MariaDB 11.8 LTS |
| `THETRIANGLE-MAXSCALE` | `10.248.40.183` | proxy | MaxScale 24.02 |
| DB2 | not provisioned | replica | — |

Both are unprivileged **LXC containers**, 4 vCPU / 4 GB / 63 GB, installed from
apt. Docker was deliberately not used: it needs Proxmox-side nesting on an
unprivileged container and costs ~300 MB of a 4 GB budget.

## What this directory is

These `.cnf` files are the **source of truth for what is installed on those
hosts** — edit here, copy up, restart the service.

| File | Installed as | Host |
| --- | --- | --- |
| [primary.cnf](primary.cnf) | `/etc/mysql/mariadb.conf.d/70-triangle-primary.cnf` | DB1 |
| [replica.cnf](replica.cnf) | `/etc/mysql/mariadb.conf.d/70-triangle-replica.cnf` | DB2, when it exists |
| [../maxscale/maxscale.cnf](../maxscale/maxscale.cnf) | `/etc/maxscale.cnf` | MaxScale |
| [setup-replica.sh](setup-replica.sh) | run once on DB2 | DB2, when it exists |

**The `70-` prefix is load-bearing.** MariaDB reads `mariadb.conf.d/` in lexical
order and Ubuntu's stock `50-server.cnf` sets `bind-address = 127.0.0.1`. A file
sorting before it cannot override that, and the primary would be unreachable
from MaxScale.

## Installing

MariaDB (DB1) is the standard `deb.mariadb.org` repo. Note that it carries
**only LTS lines** — 10.6, 10.11, 11.4, 11.8. 11.7 was a short-term release and
has been withdrawn, so 11.8 is the floor here; the configs were re-verified
against 11.8 before install.

MaxScale needs its **own GPG key**, rotated 2025-12-10 — neither the MariaDB
Server key nor the Enterprise key will validate it:

```
https://supplychain.mariadb.com/MariaDB-MaxScale-GPG-KEY   # BB2A36F3…5D87FACA8C27D14E
https://dlm.mariadb.com/repo/maxscale/latest/apt jammy main
```

## Secrets

Nothing here is committed, and the passwords were generated on-host and have
never left the boxes.

- **DB1** — `/root/triangle-db-credentials.env` (0600) holds the app,
  replication, and MaxScale passwords. `APP_PASSWORD` is what Delta's `cms.env`
  needs as `DB_PASSWORD`.
- **MaxScale** — `/etc/maxscale.cnf` is world-readable 0644, so backend
  addresses and the service password live in
  `/etc/maxscale.secrets.d/backend.env` (0640 `root:maxscale`) and reach the
  process through the systemd drop-in
  `/etc/systemd/system/maxscale.service.d/10-backend-env.conf`:

  ```
  PRIMARY_HOST=10.248.40.154
  REPLICA_HOST=192.0.2.2        # RFC 5737 placeholder until DB2 exists
  MARIADB_PORT=3306
  MAXSCALE_USER=maxscale
  MAXSCALE_PASSWORD=...
  ```

## Accounts on DB1

| Account | Grants | Why |
| --- | --- | --- |
| `triangle_user@10.248.40.183` | `ALL PRIVILEGES ON triangle.*` | the app, via MaxScale |
| `triangle_user@10.248.40.168` | `ALL PRIVILEGES ON triangle.*` | Delta direct; cutover verification only, droppable |
| `maxscale@10.248.40.183` | monitor + account-table reads | no failover privileges |
| `repl@'%'` | `REPLICATION SLAVE` | pending DB2 |

`ALL PRIVILEGES` rather than DML-only because the CMS runs additive DDL
(`ADD COLUMN IF NOT EXISTS`) at startup.

## Adding the replica (DB2)

1. Provision the host, install MariaDB 11.8, install `replica.cnf` as
   `70-triangle-replica.cnf` **with `bind-address` set** to DB2's NIC.
2. Run [setup-replica.sh](setup-replica.sh) on DB2 — it takes a GTID-consistent
   `--single-transaction` dump from DB1 (non-blocking; DB1 keeps serving),
   restores it, and starts replication. Pass `DUMP_USER`/`DUMP_PASSWORD` for a
   privileged account: dumping as `repl` fails, because `REPLICATION SLAVE`
   grants the binlog stream but no table reads.
3. Point `REPLICA_HOST` in MaxScale's `backend.env` at DB2 and restart MaxScale.
   `maxscale.cnf` already carries the two-server topology, so no config edit is
   needed.
4. Confirm `maxctrl list servers` shows `Master, Running` and `Slave, Running`.

## Verifying the split

```
maxctrl list servers     # roles + replication lag
maxctrl list services    # connections/routing
maxctrl list sessions    # which backend a live session is on
```

Reads should land on the replica, writes — and reads-after-writes within a
session — on the primary. `causal_reads=local` makes MaxScale wait for the
replica to reach the write's GTID, so a session always sees its own writes
despite lag and **no app-level split is needed**.

## Enabling automated failover (optional)

`auto_failover` is **off**. Promoting a replica on a 2-node async pair risks
split-brain and data loss, and should be a deliberate ops action. To enable:

1. Grant `REPLICATION SLAVE ADMIN, SUPER, PROCESS, EVENT, SET USER, RELOAD` to
   `MAXSCALE_USER` on the primary.
2. Set `auto_failover=true` in [maxscale.cnf](../maxscale/maxscale.cnf).
3. Prefer `maxctrl call command mariadbmon switchover` for planned maintenance.

Manual failover without MaxScale: on the replica `STOP SLAVE; RESET SLAVE ALL;`,
set `read_only=OFF`, repoint `PRIMARY_HOST`, rebuild the old primary as a replica.

## Notes / gotchas

- **MaxScale mis-sizes its cache on LXC.** The container's cgroup `memory.max`
  reads `max`, so MaxScale ignores lxcfs's 4 GB `/proc/meminfo` and takes 15% of
  the *Proxmox host's* ~62 GB — it sized the query classifier cache at 9.38 GiB
  on a 4 GB box. `query_classifier_cache_size=64M` is now pinned explicitly.
  Any other memory-autotuning service on these containers has the same trap.
- **Both hosts need a DHCP reservation.** PVE wrote `eth0.network` with
  `DHCP = no` and no `Address=`, so the addresses originally came from one-off
  `dhclient` runs; DB1 silently lost its IPv4 when the lease expired. Static
  overrides now live at `/etc/systemd/network/10-eth0-static.network` on both
  (systemd-networkd applies the lexically first match, so `10-` beats PVE's
  file and survives regeneration) — but the addresses came out of the DHCP pool
  and can still be reassigned to someone else. MACs: DB1
  `bc:24:11:e5:cc:58`, MaxScale `bc:24:11:83:05:ea`.
- **DB1's firewall is open.** ufw is inactive and iptables is ACCEPT, so 3306 is
  reachable from the whole subnet. It should be restricted to the MaxScale host
  (and DB2 later); this was left alone deliberately to avoid an SSH lockout on a
  remote host, so do it with console access available.
- **DB1 is the only copy of the data.** The old dev container and its volume are
  gone. The pre-cutover dump on Delta at
  `~/triangle-deploy/backups/triangle-precutover-20260729-2137.sql.gz` is a
  point-in-time artifact, not a backup rotation — real backups are still owed.
- `server_id` must be unique per node (1 primary / 2 replica); `gtid_domain_id`
  must match (1 here).
- Schema changes: the CMS runs additive, idempotent migrations at startup that
  replicate cleanly. Keep DDL expand-only so a rollback never drops a column the
  old version needs. Large `ALTER`s run on the primary and replicate — watch
  replica lag during them.
- `mariadb-dump` emits `SQL_MODE='NO_AUTO_VALUE_ON_ZERO'` in its preamble, which
  is what preserves `articles.id = 0`. A hand-written `INSERT` script must set
  it manually.
