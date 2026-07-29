#!/bin/sh
# Runs once, during the PRIMARY's first initialization (docker-entrypoint-initdb.d).
# Creates the least-privilege user the replica uses to pull the binlog. Uses env
# vars so no secret is written into a committed file.
#
# Required env (from cms.env / compose): MARIADB_ROOT_PASSWORD, REPL_USER, REPL_PASSWORD.
set -eu

: "${REPL_USER:?REPL_USER is required}"
: "${REPL_PASSWORD:?REPL_PASSWORD is required}"

mariadb -uroot -p"${MARIADB_ROOT_PASSWORD}" <<SQL
CREATE USER IF NOT EXISTS '${REPL_USER}'@'%' IDENTIFIED BY '${REPL_PASSWORD}';
GRANT REPLICATION SLAVE ON *.* TO '${REPL_USER}'@'%';
FLUSH PRIVILEGES;
SQL

echo "replication user '${REPL_USER}' ready on primary"
