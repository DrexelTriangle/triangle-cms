#!/bin/sh
# One-time: provision DB2 as a read replica of DB1 and start GTID replication.
#
# Run this ON THE REPLICA HOST (as root, or under sudo) after MariaDB is
# installed there natively and replica.cnf is in place as
# /etc/mysql/mariadb.conf.d/70-triangle-replica.cnf:
#
#   PRIMARY_HOST=10.248.40.154 \
#   DUMP_USER=root DUMP_PASSWORD=... \
#   REPL_USER=repl REPL_PASSWORD=... \
#   sh setup-replica.sh
#
# Local admin goes over the unix socket as root, so no local password is needed
# (unix_socket auth is the default for root on a native apt install).
#
# Idempotency: it exits early if replication is already running, so re-running
# after a hiccup is safe. The dump records the exact primary position via
# --gtid --master-data=1 (BOTH are needed — see the dump step below), so
# START SLAVE ... MASTER_USE_GTID=slave_pos resumes with no gaps or duplicates.
set -eu

: "${PRIMARY_HOST:?PRIMARY_HOST is required (DB1's address)}"
PRIMARY_PORT="${PRIMARY_PORT:-3306}"
: "${REPL_USER:?REPL_USER is required}"
: "${REPL_PASSWORD:?REPL_PASSWORD is required}"
MARIADB_DATABASE="${MARIADB_DATABASE:-triangle}"

# The dump is taken as a SEPARATE, privileged account — NOT as REPL_USER.
# `repl` holds only REPLICATION SLAVE, which grants the binlog stream but no
# table reads, so dumping as `repl` fails with "SELECT command denied". Use
# root, or any account with SELECT/SHOW VIEW/TRIGGER/EVENT on the database.
: "${DUMP_USER:?DUMP_USER is required (an account that can SELECT the data, e.g. root)}"
: "${DUMP_PASSWORD:?DUMP_PASSWORD is required}"

DUMP_FILE="$(mktemp /var/tmp/primary-dump.XXXXXX.sql)"
chmod 600 "${DUMP_FILE}"
trap 'rm -f "${DUMP_FILE}"' EXIT INT TERM

local_sql() { mariadb "$@"; }

if local_sql -N -e "SHOW SLAVE STATUS\G" 2>/dev/null | grep -q "Slave_IO_Running: Yes"; then
  echo "replica already running; nothing to do"
  exit 0
fi

echo "waiting for primary ${PRIMARY_HOST}:${PRIMARY_PORT} ..."
until mariadb -h"${PRIMARY_HOST}" -P"${PRIMARY_PORT}" -u"${DUMP_USER}" -p"${DUMP_PASSWORD}" \
      -e "SELECT 1" >/dev/null 2>&1; do
  sleep 3
done

echo "dumping ${MARIADB_DATABASE} from primary (GTID-consistent) ..."
# --master-data=1 is REQUIRED and is not optional decoration. `--gtid` on its own
# emits NOTHING: in MariaDB it only changes the FORMAT of the position recorded
# by --master-data/--dump-slave, so `--gtid` without one of those produces a dump
# carrying no replication start position at all. The replica would then begin
# from its own empty gtid_slave_pos, i.e. from the very start of the primary's
# binlogs — which are expired after binlog_expire_logs_seconds (7 days), so
# replication either dies with error 1236 or replays history on top of the
# restored data. Verified against 11.8.8: --gtid alone emits no gtid line;
# --master-data=1 emits an ACTIVE `SET GLOBAL gtid_slave_pos='1-1-...';`, which
# is what the CHANGE MASTER ... MASTER_USE_GTID=slave_pos below consumes.
# It must be =1, not =2 — =2 comments that same line out.
#
# --single-transaction keeps the dump consistent without locking InnoDB tables,
# but note that combining it with --master-data does take a brief global read
# lock at the very start, just long enough to read the binlog position. It is
# milliseconds, not the length of the dump, and the primary serves throughout.
mariadb-dump -h"${PRIMARY_HOST}" -P"${PRIMARY_PORT}" -u"${DUMP_USER}" -p"${DUMP_PASSWORD}" \
  --single-transaction --gtid --master-data=1 --routines --triggers --events \
  --databases "${MARIADB_DATABASE}" > "${DUMP_FILE}"

# Fail loudly here rather than starting replication from a bogus position.
if ! grep -q "^SET GLOBAL gtid_slave_pos=" "${DUMP_FILE}"; then
  echo "ERROR: dump contains no active 'SET GLOBAL gtid_slave_pos=' line." >&2
  echo "Replication would start from the wrong position. Check that" >&2
  echo "${DUMP_USER} holds RELOAD/BINLOG MONITOR on the primary." >&2
  exit 1
fi

echo "loading dump into replica ..."
# read_only=ON is set in replica.cnf; root is exempt (it holds SUPER), so the
# restore lands without having to relax it.
local_sql < "${DUMP_FILE}"

echo "starting replication ..."
local_sql <<SQL
STOP SLAVE;
CHANGE MASTER TO
  MASTER_HOST='${PRIMARY_HOST}',
  MASTER_PORT=${PRIMARY_PORT},
  MASTER_USER='${REPL_USER}',
  MASTER_PASSWORD='${REPL_PASSWORD}',
  MASTER_USE_GTID=slave_pos;
START SLAVE;
SQL

sleep 2
local_sql -e "SHOW SLAVE STATUS\G" | grep -E "Slave_IO_Running|Slave_SQL_Running|Seconds_Behind_Master|Last_.*Error" || true
echo
echo "done. Verify 'Slave_IO_Running: Yes' and 'Slave_SQL_Running: Yes' above."
echo
echo "NEXT, in order — see 'Bringing up DB2' in README.md. Do NOT enable"
echo "auto_failover until semi-sync is confirmed engaged and the write path is"
echo "fenced; promoting an async replica loses acknowledged writes."
echo
echo " 1. Enable semi-sync on the PRIMARY (dynamic, no restart needed):"
echo "      SET GLOBAL rpl_semi_sync_master_enabled       = ON;"
echo "      SET GLOBAL rpl_semi_sync_master_wait_point    = AFTER_SYNC;"
echo "      SET GLOBAL rpl_semi_sync_master_timeout       = 1000;"
echo "      SET GLOBAL rpl_semi_sync_master_wait_no_slave = OFF;"
echo "    replica.cnf already sets rpl_semi_sync_slave_enabled=ON here, and the"
echo "    CHANGE MASTER above reconnected the IO thread, so this node registers"
echo "    as a semi-sync client as soon as the primary is enabled."
echo
echo " 2. CONFIRM it engaged, on the primary — do not assume:"
echo "      SHOW STATUS LIKE 'Rpl_semi_sync_master_status';   -- must be ON"
echo "      SHOW STATUS LIKE 'Rpl_semi_sync_master_clients';  -- must be 1"
echo "    Healthy replication with status OFF means it degraded to async."
echo
echo " 3. Copy the app/monitor/repl accounts to THIS host. The dump above is"
echo "    --databases triangle, which EXCLUDES mysql.*, so any account created"
echo "    before replication started is missing here and a promotion would"
echo "    yield a server nothing can log in to. Apply them under"
echo "    SET SESSION sql_log_bin=0 so they stay out of the binlog."
echo
echo " 4. Fence the write path AT THE NETWORK: firewall 3306 to the MaxScale"
echo "    host and the DB peer, adding 'ufw allow 22/tcp' BEFORE enabling."
echo "    Do NOT drop triangle_user@<delta> to 'close a bypass' — MaxScale"
echo "    authenticates clients by their own source address, so that account"
echo "    is how the CMS logs in through MaxScale. Dropping it just breaks"
echo "    the site. Have console access open."
echo
echo " 5. On the MaxScale host, set REPLICA_HOST in"
echo "    /etc/maxscale.secrets.d/backend.env to this node (it is the RFC 5737"
echo "    placeholder 192.0.2.2 until then), grant the maxscale user"
echo "    REPLICATION SLAVE ADMIN, SUPER, PROCESS, EVENT, SET USER, RELOAD,"
echo "    copy up maxscale.cnf, and 'systemctl restart maxscale'."
echo
echo " 6. Confirm 'maxctrl list servers' shows Master, Running and Slave,"
echo "    Running — then test a real failover before relying on it."
