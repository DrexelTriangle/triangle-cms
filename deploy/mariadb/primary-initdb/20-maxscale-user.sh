#!/bin/sh
# Runs once, during the PRIMARY's first init. Creates the MaxScale service user
# and replicates to the read replica via the binlog, so MaxScale can authenticate
# CMS clients against the backends and monitor replication topology.
#
# Required env: MARIADB_ROOT_PASSWORD, MAXSCALE_USER, MAXSCALE_PASSWORD.
set -eu

: "${MAXSCALE_USER:?MAXSCALE_USER is required}"
: "${MAXSCALE_PASSWORD:?MAXSCALE_PASSWORD is required}"

mariadb -uroot -p"${MARIADB_ROOT_PASSWORD}" <<SQL
CREATE USER IF NOT EXISTS '${MAXSCALE_USER}'@'%' IDENTIFIED BY '${MAXSCALE_PASSWORD}';

-- Client authentication: MaxScale reads the account tables to auth CMS clients.
GRANT SELECT ON mysql.user TO '${MAXSCALE_USER}'@'%';
GRANT SELECT ON mysql.db TO '${MAXSCALE_USER}'@'%';
GRANT SELECT ON mysql.tables_priv TO '${MAXSCALE_USER}'@'%';
GRANT SELECT ON mysql.columns_priv TO '${MAXSCALE_USER}'@'%';
GRANT SELECT ON mysql.procs_priv TO '${MAXSCALE_USER}'@'%';
GRANT SELECT ON mysql.proxies_priv TO '${MAXSCALE_USER}'@'%';
GRANT SELECT ON mysql.roles_mapping TO '${MAXSCALE_USER}'@'%';
GRANT SHOW DATABASES ON *.* TO '${MAXSCALE_USER}'@'%';

-- mariadbmon monitor + enforce_read_only_slaves.
GRANT BINLOG MONITOR, SLAVE MONITOR, READ_ONLY ADMIN, RELOAD ON *.* TO '${MAXSCALE_USER}'@'%';

-- NOTE: automated failover (auto_failover=true) additionally needs
-- REPLICATION SLAVE ADMIN, SUPER, PROCESS, EVENT, SET USER, RELOAD. Grant those
-- only if/when you enable failover — see deploy/mariadb/README.md.
FLUSH PRIVILEGES;
SQL

echo "maxscale user '${MAXSCALE_USER}' ready on primary"
