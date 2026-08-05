# Production MariaDB — primary + MaxScale (native installs, off Delta)

The database tier runs on its own hosts, **not** on Delta and **not** in Docker.
The CMS opens a single connection to the endpoint configured as
`DB_HOST`/`DB_PORT` in Delta's host-only `cms.env`; that endpoint is MaxScale,
which splits writes to the primary and reads to the replica.

```
 CMS / app host (Delta)            DB primary host          DB replica host
 ┌───────────────────────┐         ┌────────────────┐       ┌────────────────┐
 │ cms-blue   cms-green  │  writes │ DB1            │ GTID  │ DB2            │
 │      │        │       │────────▶│  (server_id 1) │──────▶│  (server_id 2) │
 │      └────┬───┘       │         │  binlog+ACID   │◀──ack─│  read_only     │
 └───────────┼───────────┘  reads  └────────────────┘ semi- └────────────────┘
             └──────────▶ MaxScale (rwsplit :4006) ─────sync──────▶
                          mariadbmon: auto_failover
```

| Host | Address | CT | Role | Software |
| --- | --- | --- | --- | --- |
| `THETRIANGLE-DB1-LXC` | `10.248.40.154` | 108 | primary | MariaDB 11.8 LTS |
| `THETRIANGLE-DB2-LXC` | `10.248.40.155` | 111 | replica / failover target | MariaDB 11.8 LTS |
| `THETRIANGLE-MAXSCALE` | `10.248.40.183` | 109 | proxy | MaxScale 24.02 |

All three are unprivileged **LXC containers**, 4 vCPU / 4 GB / 63 GB, installed
from apt. Docker was deliberately not used: it needs Proxmox-side nesting on an
unprivileged container and costs ~300 MB of a 4 GB budget.

> **DB2 was created ~2026-08-03 with DB1's address**, `10.248.40.154`, and both
> were live on the bridge — which host you reached depended on which ARP entry
> won on your path. It has since been renumbered to `10.248.40.155`
> (MAC `bc:24:11:71:f9:f8`). Confirm `hostname` before trusting the output of
> anything you send to a DB host.

## What this directory is

These `.cnf` files are the **source of truth for what is installed on those
hosts** — edit here, copy up, restart the service.

| File | Installed as | Host |
| --- | --- | --- |
| [primary.cnf](primary.cnf) | `/etc/mysql/mariadb.conf.d/70-triangle-primary.cnf` | DB1 |
| [replica.cnf](replica.cnf) | `/etc/mysql/mariadb.conf.d/70-triangle-replica.cnf` | DB2 |
| [../maxscale/maxscale.cnf](../maxscale/maxscale.cnf) | `/etc/maxscale.cnf` | MaxScale |
| [provision-db2.sh](provision-db2.sh) | run once on DB2 | DB2 |
| [setup-replica.sh](setup-replica.sh) | run once on DB2, after the above | DB2 |

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
  REPLICA_HOST=10.248.40.155
  MARIADB_PORT=3306
  MAXSCALE_USER=maxscale
  MAXSCALE_PASSWORD=...
  ```

## Accounts on DB1

| Account | Grants | Why |
| --- | --- | --- |
| `triangle_user@10.248.40.168` | `ALL PRIVILEGES ON triangle.*` | **client-side auth**: lets Delta log in *through* MaxScale |
| `triangle_user@10.248.40.183` | `ALL PRIVILEGES ON triangle.*` | **backend-side auth**: lets MaxScale open the backend connection |
| `maxscale@10.248.40.183` | monitor, account reads, **+ failover admin** | promotes/demotes on failover |
| `repl@10.248.40.155` | `REPLICATION SLAVE` | DB2's replication link |
| `repl@10.248.40.154` | `REPLICATION SLAVE` | reverse link, for `auto_rejoin` after failover |

`ALL PRIVILEGES` rather than DML-only because the CMS runs additive DDL
(`ADD COLUMN IF NOT EXISTS`) at startup.

**Both `triangle_user` rows are required — do not "clean up" the Delta-scoped
one.** MaxScale authenticates a client against the backend user table by the
**client's own source address**, then connects to the backend from its own. Drop
either and the CMS gets `Error 1045`. See the warning in step 6.

**Every one of these must exist on DB2 as well**, because on promotion DB2 serves
the application and MaxScale re-authenticates everything against *its* user
table. **`setup-replica.sh` does NOT copy them** — it dumps `--databases
triangle`, which excludes `mysql.*` — so accounts created *before* replication
started are absent on DB2, and only those created *after* replicate. Verify with
`SELECT CONCAT(user,'@',host) FROM mysql.user` on both hosts; a mismatch here
means a "successful" failover promotes a server nothing can log in to. To backfill
without polluting the replication stream, apply them on DB2 under
`SET SESSION sql_log_bin=0`.

## Bringing up DB2

Ordered so the cluster is never in a state where automated failover could fire
at a replica that is not ready. **Do not enable `auto_failover` before step 6.**

**Prerequisite — `tadmin` has no passwordless sudo on DB2.** It is in the `sudo`
group but no `NOPASSWD` rule exists, unlike DB1 and MaxScale. Either add one to
match the other two hosts, or run steps 1–3 from the Proxmox console.

0. **Take a backup of DB1 first.** DB1 is still the only copy of the data (see
   Notes). Automated failover is a mechanism for *promoting* a server, not a
   substitute for being able to restore one.
   ✅ *Done 2026-08-05: `/var/backups/triangle/triangle-predb2-20260805-005529.sql.gz`
   on DB1 — 36.7 MB gzipped, 97.6 MB raw, 16 tables, `gzip -t` clean.*

1–2. **Install MariaDB 11.8 and the replica config** — run
   [provision-db2.sh](provision-db2.sh) on DB2 as root. It adds the
   `deb.mariadb.org` 11.8 repo (same series as DB1, so a promotion is not also a
   version change), installs `replica.cnf` as
   `/etc/mysql/mariadb.conf.d/70-triangle-replica.cnf`, restarts, and verifies
   `server_id=2`, the bind address, and durability. It refuses to run unless the
   hostname is `THETRIANGLE-DB2-LXC`.

   *Verified reachable from DB2: DB1:3306, MaxScale:4006, deb.mariadb.org:443.*

3. **Create the replication accounts on DB1.**
   ✅ *Done 2026-08-05: `repl@10.248.40.155` and `repl@10.248.40.154` both
   created with `REPLICATION SLAVE`, password from
   `/root/triangle-db-credentials.env`.* The `.154` one is so the **old primary
   can replicate back** from DB2 after `auto_rejoin`; without it, a rejoin fails
   on access denied.

   A pre-existing **`repl@'%'` is still present** and should be dropped — a
   wildcard replication account defeats the point of host-scoping, and DB1's
   3306 is reachable from the whole subnet until the firewall step below.

4. **Run [setup-replica.sh](setup-replica.sh) on DB2** (not on DB1). It takes a
   GTID-consistent `--single-transaction --master-data=1` dump from DB1, restores
   it, and starts replication. DB1 keeps serving throughout; the only lock is a
   brief global read lock at the start, held just long enough to read the binlog
   position:

   ```
   PRIMARY_HOST=10.248.40.154 \
   DUMP_USER=root DUMP_PASSWORD=... \
   REPL_USER=repl REPL_PASSWORD=... \
   sudo -E sh setup-replica.sh
   ```

   `DUMP_USER` must be a privileged account, **not** `repl`: `REPLICATION SLAVE`
   grants the binlog stream but no table reads, so dumping as `repl` fails with
   "SELECT command denied". The script is idempotent — it exits early if
   replication is already running.

   Confirm `Slave_IO_Running: Yes`, `Slave_SQL_Running: Yes`, and
   `Seconds_Behind_Master: 0` before continuing.

5. **Turn on semi-sync and confirm it engages.** The setting is persisted in the
   `.cnf` files, but it is dynamic, so it can be applied without a restart:

   ```sql
   -- DB1
   SET GLOBAL rpl_semi_sync_master_enabled       = ON;
   SET GLOBAL rpl_semi_sync_master_wait_point    = AFTER_SYNC;
   SET GLOBAL rpl_semi_sync_master_timeout       = 1000;
   SET GLOBAL rpl_semi_sync_master_wait_no_slave = OFF;
   -- DB2
   SET GLOBAL rpl_semi_sync_slave_enabled = ON;
   STOP SLAVE IO_THREAD; START SLAVE IO_THREAD;   -- required: the slave only
                                                  -- registers as semi-sync when
                                                  -- the IO thread reconnects
   ```

   Then on DB1, **verify rather than assume**:

   ```sql
   SHOW STATUS LIKE 'Rpl_semi_sync_master_status';      -- must be ON
   SHOW STATUS LIKE 'Rpl_semi_sync_master_clients';     -- must be 1
   SHOW STATUS LIKE 'Rpl_semi_sync_master_no_tx';       -- should stop climbing
   ```

   `Rpl_semi_sync_master_status = OFF` with replication otherwise healthy means
   it silently degraded to async — the zero-loss guarantee is not in force and
   failover is not yet safe to enable.

6. **Fence the write path — at the network, and ONLY at the network.** This is
   what removes the split-brain risk, and it is a prerequisite for step 8, not
   an optional hardening pass. Restrict 3306 on both DB hosts to MaxScale and
   the DB peer:

   ```sh
   ufw allow 22/tcp                                             # BEFORE enabling
   ufw allow from 10.248.40.183 to any port 3306 proto tcp      # MaxScale
   ufw allow from <the other DB host> to any port 3306 proto tcp # replication
   ufw --force enable
   ```

   Add the `allow` rules **before** `enable`, and do it **with Proxmox console
   access open** — `pct enter <ctid>` gets you back in if you cut yourself off.
   Do DB2 first: a mistake there costs replication, a mistake on DB1 costs the
   site.

   > ⚠️ **Do NOT "fence" this by dropping `triangle_user@10.248.40.168`.**
   > It looks like a direct-bypass account and it is not. **MaxScale
   > authenticates a client against the backend's user table using the
   > CLIENT's own source address**, so `triangle_user@<delta-ip>` is precisely
   > what lets the CMS log in *through* MaxScale; `triangle_user@<maxscale-ip>`
   > is what lets MaxScale then open the backend connection. **Both are
   > required.** Dropping the Delta-scoped one closes no bypass and takes the
   > site down with `Error 1045 Access denied for user
   > 'triangle_user'@'10.248.40.168'` on any DB-backed route, while
   > `/v1/health` keeps returning 200 — so it looks fine until someone loads a
   > page. This was done and reverted on 2026-08-05. The firewall above is what
   > actually removes the bypass, because Delta is no longer permitted to reach
   > 3306 at all.

7. **Grant MaxScale the failover privileges** it does not currently hold, on
   DB1:

   ```sql
   GRANT REPLICATION SLAVE ADMIN, SUPER, PROCESS, EVENT, SET USER, RELOAD
     ON *.* TO 'maxscale'@'10.248.40.183';
   ```

8. **Point MaxScale at DB2 and enable failover.** Set
   `REPLICA_HOST=10.248.40.155` in `/etc/maxscale.secrets.d/backend.env`
   (it is the RFC 5737 placeholder `192.0.2.2` until this is done), copy up the
   new `maxscale.cnf`, and `systemctl restart maxscale`.

   Confirm `maxctrl list servers` shows `Master, Running` **and**
   `Slave, Running` — a replica showing `Down` here means MaxScale still has the
   placeholder address.

9. **Test the failover before trusting it**, at a quiet hour, with the backup
   from step 0 in hand. See below.

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

## Why automated failover is safe here

`auto_failover` is **on**. The two standard objections to automating promotion
on a 2-node pair are real, and neither is answered by the failover setting
itself — they are answered by the replication mode and the network topology.

**"Async replication loses the tail on promotion."** True, and it is why this
was left off originally. Replication is now **semi-synchronous with
`wait_point=AFTER_SYNC`**: DB1 does not commit to the storage engine, and
therefore never acknowledges to the CMS, until DB2 has the binlog event
durably. A promoted DB2 cannot be missing a write that the application was told
succeeded. `AFTER_COMMIT` — the MariaDB default — would *not* give this: it
makes the write visible to other sessions before the ack, which is exactly the
window that loses data.

**"Two nodes can't tell 'primary is dead' from 'I can't see the primary'."**
Also true, and unfixable at that layer: a 2-node cluster has no quorum, so it
cannot vote. The risk is removed structurally instead, by making MaxScale the
**only** path to the databases — 3306 on both DB hosts is firewalled to
MaxScale and the DB peer, so nothing else can open a connection at all. Once
that holds, "MaxScale cannot reach DB1" implies "the CMS cannot reach DB1", so
promoting DB2 cannot result in two servers taking application writes. The
partition that would split-brain a quorum-less cluster instead just moves all
traffic to the promoted node, which is the desired outcome.

Note this fencing is **purely a network property**. It is tempting to also
revoke the app's Delta-scoped grant as "a second write path", but that account
is not a bypass — it is how MaxScale authenticates the client — and removing it
only breaks the site. See step 6.

**"The old primary comes back and clobbers things."** `gtid_strict_mode=ON` on
both nodes. A returning DB1 that diverged is *refused* by `auto_rejoin` and
sits there needing a human, rather than replicating conflicting history.

### What is still not covered

Honest limits, all of which need a third node to close:

- **While DB2 is down, semi-sync degrades to async** and the zero-loss guarantee
  lapses. This is deliberate — `rpl_semi_sync_master_wait_no_slave=OFF` — because
  the alternative is DB1 stalling every commit for `rpl_semi_sync_master_timeout`
  whenever DB2 is offline, turning a replica outage into a site outage. So
  DB2-down-then-DB1-dies can still lose writes. **Alert on
  `Rpl_semi_sync_master_status`** rather than assuming the guarantee holds.
- **MaxScale is a single point of failure** and the sole arbiter. If it dies,
  the CMS is down regardless of how healthy both databases are. Fencing the
  write path to MaxScale deepens this dependency — that is the price of removing
  split-brain without a quorum.
- **Failover is not backup.** Promotion protects against a host dying, not
  against a bad migration or a `DROP TABLE`, both of which replicate to DB2 in
  milliseconds.

### Planned maintenance

Use **switchover**, not failover — it demotes the old primary cleanly instead of
assuming it is dead:

```
maxctrl call command mariadbmon switchover MariaDB-Monitor replica primary
```

### Testing it

Do this once, at a quiet hour, with a fresh DB1 backup, **before** relying on it:

```
maxctrl list servers                       # baseline: Master/Slave, Running
```

Stop MariaDB on DB1 (`systemctl stop mariadb`), then watch MaxScale's log at
`/var/log/maxscale/maxscale.log`. Within ~10s (`failcount` 5 × `monitor_interval`
2000ms) it should log `master_down`, promote DB2, and `maxctrl list servers`
should show DB2 as `Master, Running`. Confirm the CMS still serves and can write.

Then start DB1 again and confirm `auto_rejoin` brings it back as
`Slave, Running`. **If it does not rejoin, that is the safety net working, not a
bug** — check for divergence before forcing anything. Finish by switching back
with the `switchover` command above so DB1 is primary again.

### Manual failover, without MaxScale

On DB2: `STOP SLAVE; RESET SLAVE ALL;` then `SET GLOBAL read_only=OFF`. Repoint
`PRIMARY_HOST` and rebuild the old primary as a replica.

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
- **`mariadb-dump --gtid` on its own records NOTHING.** It only changes the
  *format* of the position emitted by `--master-data`/`--dump-slave`, so without
  one of those the dump carries no replication start position at all. A replica
  seeded from such a dump begins at its own empty `gtid_slave_pos` — i.e. the
  start of the primary's binlogs, which expire after 7 days
  (`binlog_expire_logs_seconds`) — so replication either dies with error 1236 or
  replays history on top of the restored data. `setup-replica.sh` carried this
  bug and now passes `--master-data=1`, and hard-fails if the dump comes out
  without an active `SET GLOBAL gtid_slave_pos=` line. It must be `=1`: `=2`
  emits the same line **commented out**. Verified against 11.8.8.
- **DB1 is the only copy of the data until DB2 is replicating.** The old dev
  container and its volume are gone. The pre-cutover dump on Delta at
  `~/triangle-deploy/backups/triangle-precutover-20260729-2137.sql.gz` is a
  point-in-time artifact, not a backup rotation — real backups are still owed,
  and a replica is not one: a `DROP TABLE` reaches DB2 in milliseconds.
- **`tadmin` has no `NOPASSWD` sudo on DB2**, unlike DB1 and MaxScale. It is in
  the `sudo` group, so an interactive password works, but every scripted step in
  the bring-up runbook fails without a rule matching the other two hosts. Note
  the failure mode is quiet: `ssh ... 'sudo -n ...' ` prints "sudo: a password is
  required" to stderr while the pipeline reports success, because
  `cmd | ssh ...` returns the *local* command's exit status.
- **DB2 has no `10-eth0-static.network` override.** Its address is pinned in the
  CT config (`pct config 111`) but not inside the container, so it relies on a
  single layer where DB1 and MaxScale have two. Add the override to match.
- `server_id` must be unique per node (1 primary / 2 replica); `gtid_domain_id`
  must match (1 here).
- Schema changes: the CMS runs additive, idempotent migrations at startup that
  replicate cleanly. Keep DDL expand-only so a rollback never drops a column the
  old version needs. Large `ALTER`s run on the primary and replicate — watch
  replica lag during them.
- `mariadb-dump` emits `SQL_MODE='NO_AUTO_VALUE_ON_ZERO'` in its preamble, which
  is what preserves `articles.id = 0`. A hand-written `INSERT` script must set
  it manually.
