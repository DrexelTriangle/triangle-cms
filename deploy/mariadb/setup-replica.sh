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
# after a hiccup is safe. A GTID-consistent dump (--gtid) records the exact
# primary position, so START SLAVE ... MASTER_USE_GTID=slave_pos resumes with no
# gaps or duplicates.
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
# --gtid emits SET GLOBAL gtid_slave_pos=...; --single-transaction takes no lock
# on InnoDB, so the primary keeps serving throughout.
mariadb-dump -h"${PRIMARY_HOST}" -P"${PRIMARY_PORT}" -u"${DUMP_USER}" -p"${DUMP_PASSWORD}" \
  --single-transaction --gtid --routines --triggers --events \
  --databases "${MARIADB_DATABASE}" > "${DUMP_FILE}"

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
echo "maxscale.cnf already carries a [replica] server, so on the MaxScale host"
echo "just point REPLICA_HOST in /etc/maxscale.secrets.d/backend.env at DB2"
echo "(it is currently the RFC 5737 placeholder 192.0.2.2), then"
echo "'systemctl restart maxscale' and confirm 'maxctrl list servers' shows"
echo "the replica as Slave, Running."
