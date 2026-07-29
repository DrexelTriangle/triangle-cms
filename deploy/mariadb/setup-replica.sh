#!/bin/sh
# One-time: provision this replica from the primary and start GTID replication.
# Run INSIDE the replica container, after the primary is up and reachable:
#
#   docker compose -f compose.mariadb-replica.yml exec \
#     -e PRIMARY_HOST=10.0.0.10 -e REPL_USER=repl -e REPL_PASSWORD=... \
#     -e MARIADB_DATABASE=triangle \
#     mariadb-replica sh /opt/setup-replica.sh
#
# Idempotency: it stops as soon as replication is already running, so re-running
# after a hiccup is safe. A GTID-consistent dump (--gtid) captures the exact
# primary position, so START SLAVE ... MASTER_USE_GTID=slave_pos resumes with no
# gaps or duplicates.
set -eu

: "${PRIMARY_HOST:?PRIMARY_HOST is required}"
PRIMARY_PORT="${PRIMARY_PORT:-3306}"
: "${REPL_USER:?REPL_USER is required}"
: "${REPL_PASSWORD:?REPL_PASSWORD is required}"
: "${MARIADB_ROOT_PASSWORD:?MARIADB_ROOT_PASSWORD is required}"
: "${MARIADB_DATABASE:=triangle}"

local_sql() { mariadb -uroot -p"${MARIADB_ROOT_PASSWORD}" "$@"; }

# Already replicating? Do nothing.
if local_sql -N -e "SHOW SLAVE STATUS\G" 2>/dev/null | grep -q "Slave_IO_Running: Yes"; then
  echo "replica already running; nothing to do"
  exit 0
fi

echo "waiting for primary ${PRIMARY_HOST}:${PRIMARY_PORT} ..."
until mariadb -h"${PRIMARY_HOST}" -P"${PRIMARY_PORT}" -u"${REPL_USER}" -p"${REPL_PASSWORD}" \
      -e "SELECT 1" >/dev/null 2>&1; do
  sleep 3
done

echo "dumping ${MARIADB_DATABASE} from primary (GTID-consistent) ..."
# --gtid emits SET GLOBAL gtid_slave_pos=...; --single-transaction = no lock on InnoDB.
mariadb-dump -h"${PRIMARY_HOST}" -P"${PRIMARY_PORT}" -u"${REPL_USER}" -p"${REPL_PASSWORD}" \
  --single-transaction --gtid --routines --triggers --events \
  --databases "${MARIADB_DATABASE}" > /tmp/primary-dump.sql

echo "loading dump into replica ..."
local_sql < /tmp/primary-dump.sql
rm -f /tmp/primary-dump.sql

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
echo "done. verify Slave_IO_Running: Yes and Slave_SQL_Running: Yes above."
