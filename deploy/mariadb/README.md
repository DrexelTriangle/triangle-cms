# Production MariaDB — primary + read replica behind MaxScale

These database/proxy tiers run outside Delta. The CMS opens a single connection
to the externally managed endpoint configured as `DB_HOST`/`DB_PORT` in Delta's
host-only `cms.env`. When that endpoint is **MaxScale**, it splits writes to the
primary and reads to the replica:

```
 CMS / app host                    DB primary host        DB replica host
 ┌───────────────────────┐         ┌────────────────┐     ┌────────────────┐
 │ cms-blue   cms-green   │         │ mariadb-primary│ GTID│ mariadb-replica│
 │      │        │        │  writes │  (server_id 1) │────▶│  (server_id 2) │
 │      └────┬───┘        │────────▶│  binlog+ACID   │async│  read_only     │
 │        maxscale ───────┼──reads──┼────────────────┼────▶│                │
 │        (rwsplit :4006) │         └────────────────┘     └────────────────┘
 └───────────────────────┘
```

- **Primary** — [compose.mariadb-primary.yml](../compose.mariadb-primary.yml)
  `mariadb-primary`, tuned by [primary.cnf](primary.cnf). Full ACID
  (`innodb_flush_log_at_trx_commit=1`, `sync_binlog=1`), binary log + GTID,
  7-day binlog retention. Init scripts create the replication and MaxScale users.
- **Replica** — [compose.mariadb-replica.yml](../compose.mariadb-replica.yml)
  `mariadb-replica`, tuned by [replica.cnf](replica.cnf). `read_only`, relaxed
  durability (re-syncs from the primary on crash), parallel apply for low lag.
- **MaxScale** — if used, run it outside Delta. The sample config in
  [maxscale.cnf](../maxscale/maxscale.cnf) uses the `readwritesplit` router +
  `mariadbmon` monitor. `causal_reads=local` guarantees a session sees its own
  writes despite replica lag, so **no app-level split is needed**.

Buffer pool is sized for **dedicated 8 GB** DB hosts (`innodb_buffer_pool_size=5G`).
Lower it if a node shares its host.

## Secrets (`cms.env`, never committed)

```
MARIADB_ROOT_PASSWORD=...
MARIADB_PASSWORD=...                 # app user (triangle_user) password
REPL_USER=repl
REPL_PASSWORD=...
MAXSCALE_USER=maxscale
MAXSCALE_PASSWORD=...
MARIADB_BIND_ADDR=10.0.0.10          # primary internal NIC IP
MARIADB_REPLICA_BIND_ADDR=10.0.0.11  # replica internal NIC IP
PRIMARY_HOST=10.0.0.10               # what MaxScale dials for the primary
REPLICA_HOST=10.0.0.11               # what MaxScale dials for the replica
```

**Firewall:** primary 3306 reachable only from the replica host and the MaxScale
host; replica 3306 reachable only from the MaxScale host. Replication and
proxy↔backend traffic are unencrypted here — keep them on the trusted internal
network (or add TLS).

## Bring-up order

1. **Primary** (DB primary host) — creates the `repl` and `maxscale` users on
   first init:
   ```
   docker compose -f compose.mariadb-primary.yml --env-file cms.env up -d
   ```

2. **Replica** (DB replica host):
   ```
   docker compose -f compose.mariadb-replica.yml --env-file cms.env up -d
   docker compose -f compose.mariadb-replica.yml exec \
     -e PRIMARY_HOST=$MARIADB_BIND_ADDR -e REPL_USER=$REPL_USER \
     -e REPL_PASSWORD=$REPL_PASSWORD -e MARIADB_DATABASE=triangle \
     mariadb-replica sh /opt/setup-replica.sh
   ```
   Verify `SHOW SLAVE STATUS\G` → `Slave_IO_Running: Yes`, `Slave_SQL_Running: Yes`.

3. **MaxScale / DB proxy** (not on Delta):
   ```
   maxctrl list servers
   ```
   `maxctrl list servers` should show the primary as `Master, Running` and the
   replica as `Slave, Running`. Then configure Delta's `DB_HOST`/`DB_PORT` to
   this endpoint and deploy a CMS slot.

## Verifying the split

```
maxctrl list services   # connections/routing
maxctrl list servers    # roles + replication lag
```
Reads should land on the replica, writes (and reads-after-writes within a
session) on the primary.

## Enabling automated failover (optional)

`auto_failover` is **off** by default — promoting a replica on a 2-node async pair
risks split-brain/data loss and should be deliberate. To enable:

1. Grant the extra privileges on the primary (replicate to replica):
   `REPLICATION SLAVE ADMIN, SUPER, PROCESS, EVENT, SET USER, RELOAD` to `MAXSCALE_USER`.
2. Set `auto_failover=true` in [maxscale.cnf](../maxscale/maxscale.cnf).
3. Consider `maxctrl` switchover for planned maintenance instead of relying on
   auto-failover.

Manual failover without MaxScale: on the replica `STOP SLAVE; RESET SLAVE ALL;`,
set `read_only=OFF`, repoint MaxScale's `primary` server, rebuild the old primary
as a replica.

## Notes / gotchas

- `server_id` must be unique per node (1 primary / 2 replica); `gtid_domain_id`
  must match (1 here).
- Schema changes: the CMS runs additive, idempotent migrations at startup
  (`ADD COLUMN IF NOT EXISTS`) that replicate cleanly. Keep DDL expand-only so a
  rollback never drops a column the old version needs. Large `ALTER`s run on the
  primary and replicate — watch replica lag during them.
- Adding more replicas: give each a unique `server_id`, provision with
  `setup-replica.sh`, and add it to `servers=` in the monitor + service.
