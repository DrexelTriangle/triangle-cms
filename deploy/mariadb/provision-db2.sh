#!/bin/sh
# One-time: install MariaDB 11.8 on DB2 and put the replica config in place.
#
# Run this ON DB2 (THETRIANGLE-DB2-LXC, 10.248.40.155) as root:
#
#   sudo sh provision-db2.sh
#
# It does NOT start replication and does NOT touch MaxScale — it only gets a
# correctly-configured, correctly-bound MariaDB running. Run setup-replica.sh
# afterwards, then follow "Bringing up DB2" in README.md from step 5.
#
# Idempotent: safe to re-run. Existing repo/key/config are refreshed in place
# and apt skips packages already at the right version.
#
# NOTE ON ACCESS: tadmin has no NOPASSWD sudo on DB2 (unlike DB1 and MaxScale),
# so this cannot be driven over ssh non-interactively until such a rule exists:
#   echo 'tadmin ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/90-tadmin
#   chmod 440 /etc/sudoers.d/90-tadmin
set -eu

EXPECT_HOST=THETRIANGLE-DB2-LXC
EXPECT_ADDR=10.248.40.155
SERIES=11.8                      # LTS. deb.mariadb.org carries ONLY LTS lines.
CNF_SRC="$(dirname "$0")/replica.cnf"
CNF_DST=/etc/mysql/mariadb.conf.d/70-triangle-replica.cnf

# --- Guards ------------------------------------------------------------------
# DB2 was briefly live on DB1's address (10.248.40.154) and ssh gives no
# host-key warning when the ARP winner changes underneath you. Never let this
# script run against the primary: it would overwrite the primary's config with
# a read_only replica config.
[ "$(id -u)" = 0 ] || { echo "must run as root" >&2; exit 1; }
if [ "$(hostname)" != "$EXPECT_HOST" ]; then
  echo "REFUSING: hostname is '$(hostname)', expected '$EXPECT_HOST'." >&2
  echo "You are not on DB2. Check which host you actually reached." >&2
  exit 1
fi
if ! ip -4 addr show | grep -q "inet ${EXPECT_ADDR}/"; then
  echo "REFUSING: ${EXPECT_ADDR} is not configured on this host." >&2
  exit 1
fi
[ -f "$CNF_SRC" ] || { echo "cannot find replica.cnf next to this script" >&2; exit 1; }

echo "==> Host verified: $(hostname) / ${EXPECT_ADDR}"

# --- MariaDB apt repo ---------------------------------------------------------
# Note this is the SERVER key. MaxScale uses a different key entirely and is not
# installed here — see README.md.
echo "==> Adding MariaDB ${SERIES} repository"
apt-get update -qq
apt-get install -y -qq curl gpg apt-transport-https ca-certificates

install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://supplychain.mariadb.com/MariaDB-Server-GPG-KEY \
  | gpg --dearmor --yes -o /etc/apt/keyrings/mariadb.gpg
chmod 0644 /etc/apt/keyrings/mariadb.gpg

. /etc/os-release
cat > /etc/apt/sources.list.d/mariadb.list <<EOF
deb [signed-by=/etc/apt/keyrings/mariadb.gpg] https://deb.mariadb.org/${SERIES}/ubuntu ${VERSION_CODENAME} main
EOF

apt-get update -qq

# --- Install ------------------------------------------------------------------
# Pinned to the series, not "latest", so DB2 does not drift ahead of DB1 — a
# promotion should not also be a version change.
echo "==> Installing mariadb-server"
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq mariadb-server mariadb-client
mariadbd --version

# --- Config -------------------------------------------------------------------
# The 70- prefix is load-bearing: Ubuntu's stock 50-server.cnf sets
# bind-address = 127.0.0.1 and mariadb.conf.d is read in lexical order, so a
# file sorting before it cannot override the bind and the replica would be
# unreachable from both MaxScale and the primary.
echo "==> Installing ${CNF_DST}"
install -o root -g root -m 0644 "$CNF_SRC" "$CNF_DST"

echo "==> Restarting mariadb"
systemctl enable --now mariadb
systemctl restart mariadb

# --- Verify -------------------------------------------------------------------
echo "==> Verifying"
mariadb -N -B -e "SELECT @@hostname, @@server_id, @@read_only, @@gtid_domain_id, @@gtid_strict_mode"

# server_id must differ from DB1's (1) or replication refuses to start.
SID=$(mariadb -N -B -e "SELECT @@server_id")
[ "$SID" = "2" ] || { echo "FAIL: server_id is ${SID}, expected 2" >&2; exit 1; }

# The whole point of the 70- prefix. If this shows 127.0.0.1, the config did not
# take and nothing downstream will work.
echo "--- listening sockets ---"
ss -ltnp 2>/dev/null | grep 3306 || echo "WARNING: nothing listening on 3306"
if ! ss -ltn 2>/dev/null | grep -q "${EXPECT_ADDR}:3306\|0.0.0.0:3306\|\*:3306"; then
  echo "FAIL: not bound to ${EXPECT_ADDR}:3306 — check ${CNF_DST} ordering" >&2
  exit 1
fi

# Durability must match the primary: DB2 is a failover target, not a read cache.
echo "--- durability (must be 1 / 1) ---"
mariadb -N -B -e "SELECT @@innodb_flush_log_at_trx_commit, @@sync_binlog"

echo
echo "OK. MariaDB ${SERIES} is installed, bound to ${EXPECT_ADDR}, and read_only."
echo "NEXT: run setup-replica.sh on this host to seed from DB1 and start"
echo "replication, then continue at README.md 'Bringing up DB2' step 5."
