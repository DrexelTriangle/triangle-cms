#!/usr/bin/env python3
"""Load an anonymized production snapshot into the local dev database.

The alternative to reseed_from_etl.py for contributors without the WordPress
ETL. Maintainers produce the snapshot elsewhere; this only consumes it:

  0. refuse outright on a Triangle fleet host (by hostname or fleet address)
  1. validate the manifest and the SQL's sha256, and scan the SQL for anything
     that is not plain table data (USE, GRANT, cross-database names, client
     commands such as \\! or source, LOAD DATA, foreign DEFINERs)
  2. after confirmation, dump the current database to db-backups/ (unless it
     is empty or --no-backup), then DROP and recreate it inside the compose
     mariadb container and load the dump as the app user
  3. check the anonymization guarantees and the row counts against the manifest
  4. start the CMS so its startup migrations run, and check again

The volume is kept on purpose. Deleting it (what reseed does) makes MariaDB
replay the ETL seed files mounted into docker-entrypoint-initdb.d, so you would
get stale or half-missing ETL data underneath the snapshot. DROP DATABASE wipes
exactly the app database and nothing replays. The root and app users and their
grants live in the `mysql` schema, so they survive.

Until every check passes the database carries a `_snapshot_import_unverified`
table, and the CMS is left stopped on any failure.

  python ./scripts/import_snapshot.py triangle-snapshot-20261001T040000Z.sql.gz
  python ./scripts/import_snapshot.py --manifest path/to/triangle-snapshot-<ts>.manifest.json
  python ./scripts/import_snapshot.py <snapshot> --yes     # no confirmation prompt
  python ./scripts/import_snapshot.py <snapshot> --no-backup  # skip the pre-import dump
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import ipaddress
import json
import os
import re
import socket
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable, Iterable, Mapping, NamedTuple

from reseed_from_etl import (
    ROOT_DIR,
    VERIFY_TABLES,
    Fail,
    info,
    mariadb_query,
    resolve_compose,
    run,
    shell_quote,
    step,
    wait_cms_healthy,
    warn,
)

MANIFEST_FORMAT = 1
SQL_SUFFIX = ".sql.gz"
MANIFEST_SUFFIX = ".manifest.json"
MARKER_TABLE = "_snapshot_import_unverified"

IDENT_RE = re.compile(r"^[A-Za-z0-9_]{1,64}$")
SYSTEM_SCHEMAS = {"mysql", "information_schema", "performance_schema", "sys"}

# The producer's anonymization guarantees. Checked after the load, and again
# after the CMS has run its startup migrations.
MUST_BE_EMPTY = ("cms_users", "cms_sessions", "cms_activity", "classifieds")
ANONYMIZATION_CHECKS = (
    (
        "authors",
        "authors with an email, or a login other than author-<id>",
        "SELECT COUNT(*) FROM `authors` "
        "WHERE `email` IS NOT NULL OR `login` IS NULL OR `login` <> CONCAT('author-', `id`)",
    ),
    (
        "comments",
        "comments that are not approved or still carry author email/ip/url",
        "SELECT COUNT(*) FROM `comments` WHERE `status` IS NULL OR `status` <> 'approved' "
        "OR `author_email` IS NOT NULL OR `author_ip` IS NOT NULL OR `author_url` IS NOT NULL",
    ),
    (
        "articles",
        "articles that are unpublished (pub_date NULL) or archived",
        "SELECT COUNT(*) FROM `articles` WHERE `pub_date` IS NULL OR `archived_at` IS NOT NULL",
    ),
)

# Shown at the confirmation prompt: what the local database loses.
CONFIRM_TABLES = (*VERIFY_TABLES, "cms_users", "cms_sessions", "cms_settings", "media")

# Runs inside the mariadb container, as the app user (whose grants cover only
# its own database). --binary-mode turns off the client's backslash and named
# commands (\!, source, system...), so even a missed one is sent to the server
# as SQL instead of being run by the client; --local-infile=0 stops LOAD DATA
# LOCAL reading files inside the container. MYSQL_PWD keeps the password off
# the process list.
IMPORT_SHELL = (
    'MYSQL_PWD="$MARIADB_PASSWORD" exec mariadb -u"$MARIADB_USER" --binary-mode --local-infile=0 '
    '--default-character-set=utf8mb4 --max-allowed-packet=1G "$MARIADB_DATABASE"'
)

LOCAL_DOCKER_SCHEMES = ("unix://", "npipe://")

# The fleet, from docs/HANDOVER.md ("What runs where") and deploy/README.md.
# This importer is for contributor machines; on any of these it refuses, with
# no override. Hostnames compare case-insensitively, without the domain.
FLEET_HOSTNAMES = frozenset({
    "thetriangle-delta",      # VM 105, CMS app host and deploy runner
    "thetriangle-db1-lxc",    # CT 108, MariaDB primary
    "thetriangle-maxscale",   # CT 109, MaxScale proxy
    "thetriangle-wordpress",  # VM 100, legacy WordPress
})
FLEET_NETWORKS = tuple(
    ipaddress.ip_network(net)
    for net in (
        "10.248.40.0/24",
        "10.248.41.0/24",
        "10.248.42.122/32",  # CT 106, legacy WordPress database
    )
)
# `ip` on Linux, `ifconfig` where there is no iproute2 (macOS, BSD).
ADDRESS_COMMANDS = (["ip", "-o", "-4", "addr", "show"], ["ifconfig"])
_INET_RE = re.compile(r"\binet (?:addr:)?(\d{1,3}(?:\.\d{1,3}){3})")

# Pre-import backup, as root (it needs the routines, triggers and events too).
# --add-drop-database --databases makes the dump restore the database whole,
# replacing whatever the snapshot left there.
BACKUP_DIR = ROOT_DIR / "db-backups"
DUMP_SHELL = (
    'MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb-dump -uroot --single-transaction '
    "--routines --triggers --events --hex-blob --add-drop-database "
    '--default-character-set=utf8mb4 --max-allowed-packet=1G --databases "$MARIADB_DATABASE"'
)
DUMP_COMPLETE = b"-- Dump completed"
RESTORE_SHELL = 'MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb -uroot --default-character-set=utf8mb4 --max-allowed-packet=1G'


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Load an anonymized production snapshot into the local dev database (destructive).",
    )
    parser.add_argument(
        "snapshot",
        nargs="?",
        help=f"triangle-snapshot-<ts>{SQL_SUFFIX}; the manifest is read from alongside it",
    )
    parser.add_argument(
        "--manifest",
        help=f"the snapshot's {MANIFEST_SUFFIX} (default: next to the snapshot); "
        "on its own, the snapshot is read from alongside it",
    )
    parser.add_argument(
        "--yes",
        "-y",
        action="store_true",
        help="skip the destructive-action confirmation prompt",
    )
    parser.add_argument(
        "--no-backup",
        action="store_true",
        help="do not dump the current database to db-backups/ before dropping it",
    )
    args = parser.parse_args(argv)
    if not args.snapshot and not args.manifest:
        parser.error("give the snapshot .sql.gz, or --manifest")
    return args


def resolve_paths(args: argparse.Namespace) -> tuple[Path, Path]:
    if args.snapshot:
        sql_path = Path(args.snapshot).expanduser()
        if not sql_path.name.endswith(SQL_SUFFIX):
            raise Fail(f"{sql_path.name} is not a {SQL_SUFFIX} snapshot.")
    else:
        manifest = Path(args.manifest).expanduser()
        if not manifest.name.endswith(MANIFEST_SUFFIX):
            raise Fail(f"{manifest.name} is not a {MANIFEST_SUFFIX} manifest.")
        sql_path = manifest.with_name(manifest.name[: -len(MANIFEST_SUFFIX)] + SQL_SUFFIX)

    if args.manifest:
        manifest_path = Path(args.manifest).expanduser()
    else:
        manifest_path = sql_path.with_name(sql_path.name[: -len(SQL_SUFFIX)] + MANIFEST_SUFFIX)

    for path in (sql_path, manifest_path):
        if not path.is_file():
            raise Fail(f"{path} does not exist.")
    return sql_path.resolve(), manifest_path.resolve()


# --------------------------------------------------------------------------
# Manifest
# --------------------------------------------------------------------------


def _is_int(value: object) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def validate_manifest(data: object) -> dict:
    """Check a parsed manifest against format 1. Raises Fail on anything off."""
    if not isinstance(data, dict):
        raise Fail("manifest is not a JSON object.")

    fmt = data.get("format")
    if not _is_int(fmt) or fmt != MANIFEST_FORMAT:
        raise Fail(f"manifest format is {fmt!r}; this importer only understands format {MANIFEST_FORMAT}.")
    if data.get("anonymized") is not True:
        raise Fail("manifest does not say anonymized: true; refusing to load what may be raw production data.")

    created = data.get("created_at")
    try:
        if not isinstance(created, str):
            raise ValueError
        datetime.fromisoformat(created.replace("Z", "+00:00"))
    except ValueError:
        raise Fail(f"manifest created_at {created!r} is not an ISO 8601 timestamp.") from None

    commit = data.get("source_commit")
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{7,40}", commit):
        raise Fail(f"manifest source_commit {commit!r} is not a git commit hash.")

    scrub = data.get("scrub_version")
    if not isinstance(scrub, int) or isinstance(scrub, bool) or scrub < 1:
        raise Fail(f"manifest scrub_version {scrub!r} is not a positive integer.")

    sha = data.get("sql_sha256")
    if not isinstance(sha, str) or not re.fullmatch(r"[0-9a-fA-F]{64}", sha):
        raise Fail("manifest sql_sha256 is not a sha256 hex digest.")

    counts = data.get("counts")
    if not isinstance(counts, dict) or not counts:
        raise Fail("manifest counts is missing or empty.")
    for table, rows in counts.items():
        if not IDENT_RE.match(table):
            raise Fail(f"manifest counts has an invalid table name {table!r}.")
        if not _is_int(rows) or rows < 0:
            raise Fail(f"manifest counts[{table!r}] is {rows!r}, not a row count.")
    nonempty = [t for t in MUST_BE_EMPTY if counts.get(t, 0) != 0]
    if nonempty:
        raise Fail(f"manifest counts say {', '.join(nonempty)} have rows; an anonymized snapshot has none.")

    return {**data, "sql_sha256": sha.lower()}


def load_manifest(path: Path) -> dict:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise Fail(f"cannot read manifest {path.name}: {exc}") from None
    return validate_manifest(data)


def file_sha256(path: Path) -> str:
    """sha256 of the .sql.gz file as distributed (what `sha256sum` prints)."""
    digest = hashlib.sha256()
    with open(path, "rb") as fh:
        while chunk := fh.read(1 << 20):
            digest.update(chunk)
    return digest.hexdigest()


def check_sha256(path: Path, expected: str) -> None:
    actual = file_sha256(path)
    if actual != expected:
        raise Fail(f"{path.name} sha256 is {actual}, manifest says {expected}; the file is corrupt or not this snapshot.")


# --------------------------------------------------------------------------
# SQL scan
# --------------------------------------------------------------------------


class Finding(NamedTuple):
    line: int
    kind: str
    text: str


# Named commands the mariadb client runs itself when one starts a line of an
# empty statement. DELIMITER is the one mariadb-dump legitimately uses.
CLIENT_COMMANDS = {
    "?", "charset", "clear", "connect", "edit", "ego", "exit", "go", "help", "nopager",
    "notee", "nowarning", "pager", "print", "prompt", "quit", "rehash", "source",
    "status", "system", "tee", "use", "warnings",
}

_IDENT = r"(?:`(?:[^`]|``)+`|[A-Za-z_$][\w$]*)"
_QUALIFIED = rf"{_IDENT}\s*\.\s*{_IDENT}"

# Checked against a statement with string literals blanked and backticked
# identifiers replaced, so neither data nor a column called `grant` trips them.
STATEMENT_RULES = (
    ("database statement", re.compile(r"\b(?:CREATE|DROP|ALTER)\s+(?:DATABASE|SCHEMA)\b", re.I)),
    ("USE statement", re.compile(r"^\s*USE\b", re.I)),
    (
        "privilege statement",
        re.compile(
            r"\b(?:GRANT|REVOKE)\b|\b(?:CREATE|DROP|ALTER|RENAME)\s+(?:USER|ROLE)\b"
            r"|\bSET\s+(?:PASSWORD|ROLE|DEFAULT\s+ROLE)\b",
            re.I,
        ),
    ),
    (
        "file load/export",
        re.compile(r"\bLOAD\s+(?:DATA|XML)\b|\bINTO\s+(?:OUTFILE|DUMPFILE)\b|\bLOAD_FILE\s*\(", re.I),
    ),
    (
        "server-level statement",
        re.compile(
            r"\bSET\s+(?:GLOBAL|PERSIST)\b|@@GLOBAL\s*\.|\b(?:INSTALL|UNINSTALL)\s+(?:PLUGIN|SONAME)\b"
            r"|\bSHUTDOWN\b|\bCHANGE\s+MASTER\b|\b(?:CREATE|ALTER|DROP)\s+SERVER\b",
            re.I,
        ),
    ),
    (
        # Dynamic SQL runs text the scanner only ever saw as a string literal.
        "dynamic SQL or stored routine",
        re.compile(r"\b(?:PREPARE|EXECUTE|DEALLOCATE|CALL)\b|\bCREATE\b.*?\b(?:PROCEDURE|FUNCTION|EVENT)\b", re.I | re.S),
    ),
)

# The scanner splits strings the way the server does under the default
# sql_mode and a charset where 0x5c is never part of a multibyte character.
# ANSI_QUOTES, NO_BACKSLASH_ESCAPES, or SET NAMES gbk/sjis/big5 would make the
# server read the rest of the file differently, so session settings are only
# accepted in the shapes mariadb-dump writes.
SETTING_RE = re.compile(
    r"\bSQL_MODE\b|\bCHARACTER_SET_(?:CLIENT|CONNECTION|RESULTS)\b|\bCOLLATION_CONNECTION\b"
    r"|\bSET\s+(?:NAMES|CHARSET|CHARACTER\s+SET)\b",
    re.I,
)
UNSAFE_SQL_MODES = {"ANSI_QUOTES", "NO_BACKSLASH_ESCAPES", "ANSI", "DB2", "MAXDB", "MSSQL", "ORACLE", "POSTGRESQL"}
SAFE_CHARSETS = {"utf8mb4", "utf8mb3", "utf8", "latin1", "ascii", "binary", "default"}
# Where mariadb-dump saves the session settings it later restores.
SAVED_SETTING_VARS = {
    "old_character_set_client", "old_character_set_results", "old_collation_connection", "old_sql_mode",
    "saved_cs_client", "saved_cs_results", "saved_col_connection", "saved_sql_mode",
}
_MODE_ASSIGN = re.compile(r"\bSQL_MODE\s*:?=\s*([^\s,;*]+)", re.I)
_CHARSET_ASSIGN = re.compile(
    r"\b(?:CHARACTER_SET_(?:CLIENT|CONNECTION|RESULTS)|COLLATION_CONNECTION)\s*:?=\s*([^\s,;*]+)"
    r"|\bSET\s+(?:NAMES|CHARSET|CHARACTER\s+SET)\s+([^\s,;*]+)",
    re.I,
)
_VAR_ASSIGN = re.compile(r"@(\w+)\s*:?=\s*(\S+)")
MAX_TRACKED_LITERALS = 64
MAX_LITERAL_CHARS = 256

# Checked against the statement with identifiers intact. A dump of one database
# never qualifies a table with a database name, so any qualified name in a
# position that names a table is refused outright.
CROSS_DB_RE = re.compile(
    r"\b(?:INTO|UPDATE|FROM|JOIN|TABLE|TABLES|EXISTS|VIEW|TRIGGER|PROCEDURE|FUNCTION|EVENT|"
    r"SEQUENCE|REFERENCES|LIKE|TO|(?:INSERT|UPDATE|DELETE)\s+ON|INDEX\s+" + _IDENT + r"\s+ON)\s+"
    + _QUALIFIED,
    re.I,
)
# DROP TABLE a, db.b / LOCK TABLES a WRITE, db.b WRITE
LIST_CROSS_DB_RE = re.compile(r"^\s*(?:DROP|LOCK|RENAME|TRUNCATE)\b.*,\s*" + _QUALIFIED, re.I | re.S)
SYSTEM_SCHEMA_RE = re.compile(
    r"(?<![\w$`.])`?(?:" + "|".join(sorted(SYSTEM_SCHEMAS)) + r")`?\s*\.\s*" + _IDENT, re.I
)
DEFINER_RE = re.compile(r"\bDEFINER\s*=\s*((?:`(?:[^`]|``)*`|''|[^\s`'*]+)(?:\s*@\s*(?:`(?:[^`]|``)*`|''|[^\s`'*]+))?)", re.I)
ALLOWED_DEFINERS = {"CURRENT_USER", "CURRENT_USER()"}

_BACKTICKED = re.compile(r"`(?:[^`]|``)*`")
_EXEC_COMMENT = re.compile(r"M?!\d*")
_SQ_STOP = re.compile(r"[\\']")
_DQ_STOP = re.compile(r'[\\"]')
_BT_STOP = re.compile(r"`")
_COMMENT_STOP = re.compile(r"\*/")
_FIRST_WORD = re.compile(r"\s*([A-Za-z]+|\?)")
_DELIMITER_CMD = re.compile(r"\s*delimiter\s+(\S+)", re.I)


def _safe_value(value: str, allowed: Callable[[str], bool]) -> bool:
    if value.startswith("@@"):
        return True
    if value.startswith("@"):
        return value[1:].lower() in SAVED_SETTING_VARS
    return allowed(value)


def unsafe_setting(keywords: str, literals: list[str] | None) -> bool:
    """True if the statement changes how the rest of the dump would be tokenized."""
    for match in _VAR_ASSIGN.finditer(keywords):
        if match.group(1).lower() in SAVED_SETTING_VARS and not match.group(2).startswith("@@"):
            return True
    if not SETTING_RE.search(keywords):
        return False
    if literals is None:
        return True

    for match in _MODE_ASSIGN.finditer(keywords):
        if not _safe_value(match.group(1), lambda v: v == "''"):
            return True
    # Adjacent literals concatenate ('ANSI_' 'QUOTES'), so check the join too.
    for text in (*literals, "".join(literals)):
        if set(re.split(r"[,\s]+", text.upper())) & UNSAFE_SQL_MODES:
            return True

    def safe_charset(value: str) -> bool:
        return value.lower().split("_")[0] in SAFE_CHARSETS

    for match in _CHARSET_ASSIGN.finditer(keywords):
        value = match.group(1) or match.group(2)
        if value == "''":
            if not all(safe_charset(lit) for lit in literals):
                return True
        elif not _safe_value(value, safe_charset):
            return True
    return False


def check_statement(code: str, line: int, literals: list[str] | None = None) -> list[Finding]:
    """Classify one statement (string literals already blanked to '').

    literals holds the short literals' text for the session-setting check;
    None means there were too many or too long to keep.
    """
    findings: list[Finding] = []
    snippet = " ".join(code.split())[:160]
    keywords = _BACKTICKED.sub("`x`", code)
    for kind, pattern in STATEMENT_RULES:
        if pattern.search(keywords):
            findings.append(Finding(line, kind, snippet))
    if unsafe_setting(keywords, literals):
        findings.append(Finding(line, "sql_mode or charset change", snippet))
    if CROSS_DB_RE.search(code) or LIST_CROSS_DB_RE.search(code) or SYSTEM_SCHEMA_RE.search(code):
        findings.append(Finding(line, "cross-database name", snippet))
    for match in DEFINER_RE.finditer(code):
        if match.group(1).strip().upper() not in ALLOWED_DEFINERS:
            findings.append(Finding(line, "DEFINER other than CURRENT_USER", snippet))
    return findings


class SqlScanner:
    """Splits a dump into statements the way the mariadb client does, and checks each.

    Fed line by line so a multi-gigabyte dump never sits in memory: string
    literals are skipped (only a statement's first few short ones are kept, for
    the session-setting check), and comments are dropped except for
    /*! ... */ ones, whose body the server executes and so gets checked.
    """

    def __init__(self) -> None:
        self.mode = "code"  # code | squote | dquote | btick | comment
        self.exec_comment = False
        self.findings: list[Finding] = []
        self.statements = 0
        self._parts: list[str] = []
        self._nonblank = False
        self._start_line = 0
        self._literals: list[str] | None = []
        self._literal: list[str] = []
        self._literal_len = 0
        self._set_delimiter(";")

    def _set_delimiter(self, delimiter: str) -> None:
        self.delimiter = delimiter
        self._code_re = re.compile(
            "|".join([re.escape(delimiter), "'", '"', "`", r"/\*", r"\*/", r"--(?=\s|$)", "#", r"\\"])
        )

    def _emit(self, text: str, line: int) -> None:
        if not text:
            return
        if not self._nonblank and not text.isspace():
            self._nonblank = True
            self._start_line = line
        self._parts.append(text)

    def _take_literal(self, text: str) -> None:
        if self._literals is not None and self._literal_len <= MAX_LITERAL_CHARS:
            self._literal.append(text)
            self._literal_len += len(text)

    def _close_literal(self) -> None:
        if self._literals is not None:
            if self._literal_len > MAX_LITERAL_CHARS or len(self._literals) >= MAX_TRACKED_LITERALS:
                self._literals = None  # a data statement; the setting check refuses if it is also one
            else:
                self._literals.append("".join(self._literal))
        self._literal = []
        self._literal_len = 0

    def _end_statement(self) -> None:
        if self._nonblank:
            self.statements += 1
            self.findings.extend(check_statement("".join(self._parts), self._start_line, self._literals))
        self._parts = []
        self._nonblank = False
        self._literals = []

    def _line_start(self, line: str, lineno: int) -> bool:
        """Client commands recognised at the start of a line. True if the line was consumed."""
        word = _FIRST_WORD.match(line)
        if not word:
            return False
        name = word.group(1).lower()
        if name == "delimiter":
            cmd = _DELIMITER_CMD.match(line)
            if cmd:
                self._set_delimiter(cmd.group(1))
                return True
        if name in CLIENT_COMMANDS:
            self.findings.append(Finding(lineno, "client command", line.strip()[:160]))
            return True
        return False

    def feed(self, line: str, lineno: int) -> None:
        if self.mode == "code" and not self._nonblank and not self.exec_comment:
            if self._line_start(line, lineno):
                return

        pos, end = 0, len(line)
        while pos < end:
            if self.mode == "code":
                match = self._code_re.search(line, pos)
                if not match:
                    self._emit(line[pos:], lineno)
                    return
                self._emit(line[pos : match.start()], lineno)
                token, pos = match.group(0), match.end()
                if token == self.delimiter:
                    self._end_statement()
                elif token in ("'", '"'):
                    self.mode = "squote" if token == "'" else "dquote"
                    self._emit("''", lineno)
                elif token == "`":
                    self.mode = "btick"
                    self._emit("`", lineno)
                elif token == "/*":
                    executable = _EXEC_COMMENT.match(line, pos)
                    if executable and not self.exec_comment:
                        self.exec_comment = True
                        pos = executable.end()
                        self._emit(" ", lineno)
                    else:
                        self.mode = "comment"
                elif token == "*/":
                    if self.exec_comment:
                        self.exec_comment = False
                        self._emit(" ", lineno)
                    else:
                        self._emit(token, lineno)
                elif token == "#" or token.startswith("--"):
                    return
                elif token == "\\":
                    # \- is the "enable sandbox mode" line mariadb-dump writes
                    # first; it only ever restricts the client.
                    command = line[pos : pos + 1]
                    pos += 1
                    if command != "-":
                        self.findings.append(Finding(lineno, "client command", "\\" + command.strip()))
            elif self.mode in ("squote", "dquote"):
                quote = "'" if self.mode == "squote" else '"'
                match = (_SQ_STOP if quote == "'" else _DQ_STOP).search(line, pos)
                if not match:
                    self._take_literal(line[pos:])
                    return
                self._take_literal(line[pos : match.start()])
                if match.group(0) == "\\":
                    self._take_literal(line[match.end() : match.end() + 1])
                    pos = match.end() + 1
                elif line.startswith(quote, match.end()):
                    self._take_literal(quote)
                    pos = match.end() + 1
                else:
                    self._close_literal()
                    self.mode = "code"
                    pos = match.end()
            elif self.mode == "btick":
                match = _BT_STOP.search(line, pos)
                if not match:
                    self._emit(line[pos:], lineno)
                    return
                self._emit(line[pos : match.end()], lineno)
                pos = match.end()
                if line.startswith("`", pos):
                    self._emit("`", lineno)
                    pos += 1
                else:
                    self.mode = "code"
            else:  # comment
                match = _COMMENT_STOP.search(line, pos)
                if not match:
                    return
                self.mode = "code"
                pos = match.end()
                self._emit(" ", lineno)

    def finish(self, lineno: int) -> list[Finding]:
        # The client executes a trailing statement with no delimiter at EOF.
        self._end_statement()
        if self.mode != "code" or self.exec_comment:
            self.findings.append(Finding(lineno, "unterminated string or comment", self.mode))
        return self.findings


def scan_sql(lines: Iterable[str]) -> tuple[list[Finding], int]:
    scanner = SqlScanner()
    lineno = 0
    for lineno, line in enumerate(lines, 1):
        scanner.feed(line, lineno)
    return scanner.finish(lineno), scanner.statements


def scan_snapshot(path: Path) -> int:
    """Refuse the snapshot if it contains anything but plain single-database SQL."""
    try:
        with gzip.open(path, "rb") as raw:
            # latin-1 maps every byte to one character, so binary column data
            # cannot break decoding and the ASCII the scanner keys on is intact.
            text = io.TextIOWrapper(raw, encoding="latin-1", newline="")
            findings, statements = scan_sql(text)
    except (OSError, EOFError) as exc:
        raise Fail(f"{path.name} is not a readable gzip file: {exc}") from None

    if findings:
        print(f"\n  {path.name} contains statements this importer will not run:", file=sys.stderr)
        for finding in findings[:20]:
            print(f"    line {finding.line}: {finding.kind}: {finding.text}", file=sys.stderr)
        if len(findings) > 20:
            print(f"    ... and {len(findings) - 20} more", file=sys.stderr)
        raise Fail(f"refusing the snapshot: {len(findings)} forbidden statement(s); nothing was changed.")
    if statements == 0:
        raise Fail(f"{path.name} contains no SQL statements.")
    return statements


# --------------------------------------------------------------------------
# Docker
# --------------------------------------------------------------------------


def _command_output(cmd: list[str]) -> str | None:
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, check=False, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return None
    return result.stdout if result.returncode == 0 else None


def local_ipv4_addresses(output: Callable[[list[str]], str | None] = _command_output) -> list[str] | None:
    """This machine's IPv4 addresses, or None if no tool could list them."""
    for cmd in ADDRESS_COMMANDS:
        text = output(cmd)
        if text is None:
            continue
        found = _INET_RE.findall(text)
        if found:
            return found
    return None


def check_not_fleet_host(hostname: str, addresses: Iterable[str] | None) -> None:
    """Refuse on a Triangle production/fleet machine. There is no override."""
    short = hostname.strip().lower().split(".")[0]
    if short in FLEET_HOSTNAMES:
        raise Fail(f"this machine is {hostname}, a Triangle fleet host; this importer only runs on a dev machine.")
    if addresses is None:
        warn("could not list this machine's IPv4 addresses (no `ip` or `ifconfig`); "
             "skipping the fleet-address check.")
        return
    for address in addresses:
        try:
            ip = ipaddress.ip_address(address)
        except ValueError:
            continue
        for network in FLEET_NETWORKS:
            if ip in network:
                raise Fail(f"this machine has address {address}, inside the Triangle fleet network {network}; "
                           "this importer only runs on a dev machine.")


def docker_context_host() -> str | None:
    probe = run(
        ["docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}"],
        cwd=ROOT_DIR,
        capture=True,
        check=False,
    )
    return probe.stdout.strip() if probe.returncode == 0 else None


def check_local_docker(env: Mapping[str, str], context_host: Callable[[], str | None]) -> None:
    """Refuse unless both DOCKER_HOST (if set) and the active context are the local socket."""
    docker_host = env.get("DOCKER_HOST", "").strip()
    if docker_host and not docker_host.startswith(LOCAL_DOCKER_SCHEMES):
        raise Fail(f"DOCKER_HOST points at {docker_host}; this importer only runs against the local Docker socket.")

    host = (context_host() or "").strip()
    context = env.get("DOCKER_CONTEXT", "").strip() or "current"
    if not host:
        raise Fail(f"could not read the endpoint of the {context} docker context; refusing to guess where it points.")
    if not host.startswith(LOCAL_DOCKER_SCHEMES):
        raise Fail(f"the {context} docker context points at {host}; this importer only runs against the local Docker socket.")


def preflight() -> None:
    if not (ROOT_DIR / "docker-compose.yml").is_file():
        raise Fail(f"{ROOT_DIR} does not look like the triangle-cms checkout.")
    env_file = ROOT_DIR / ".env"
    if not env_file.is_file():
        raise Fail(".env is missing; compose needs MARIADB_ROOT_PASSWORD and MARIADB_PASSWORD.")
    if run(["docker", "info"], cwd=ROOT_DIR, capture=True, check=False).returncode != 0:
        raise Fail("the Docker daemon is not reachable.")


def container_database(compose: list[str]) -> str:
    """The app database name, read from the running mariadb container."""
    probe = run([*compose, "exec", "-T", "mariadb", "printenv", "MARIADB_DATABASE"], cwd=ROOT_DIR, capture=True, check=False)
    name = probe.stdout.strip()
    if probe.returncode != 0 or not name:
        raise Fail(
            "the compose mariadb container is not running. Start it with `docker start` on the "
            "existing container (see README: Load an anonymized production snapshot) and re-run."
        )
    if not IDENT_RE.match(name) or name.lower() in SYSTEM_SCHEMAS:
        raise Fail(f"refusing to replace database {name!r}.")
    if mariadb_query(compose, "SELECT 1") != "1":
        raise Fail("MariaDB is running but not answering queries yet; wait for it to be healthy and re-run.")
    return name


def root_query(compose: list[str], sql: str) -> str:
    """Run SQL as root with no default database (it may not exist, or be about to not)."""
    result = run(
        [*compose, "exec", "-T", "mariadb", "sh", "-c",
         f'MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb -uroot -N -B -e {shell_quote(sql)}'],
        cwd=ROOT_DIR,
        capture=True,
        check=False,
    )
    if result.returncode != 0:
        raise Fail(f"MariaDB rejected the statement: {result.stderr.strip()}")
    return result.stdout.strip()


def root_exec(compose: list[str], sql: str) -> None:
    root_query(compose, sql)


def sql_string(value: str) -> str:
    return "'" + value.replace("\\", "\\\\").replace("'", "''") + "'"


# --------------------------------------------------------------------------
# Pre-import backup
# --------------------------------------------------------------------------


def database_tables(compose: list[str], database: str) -> int | None:
    """How many tables the database has, or None if it does not exist."""
    name = sql_string(database)
    out = root_query(
        compose,
        f"SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = {name}; "
        f"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = {name};",
    )
    values = out.split()
    if len(values) != 2 or not all(v.isdigit() for v in values):
        raise Fail(f"could not tell whether `{database}` has any tables ({out[:80]!r}); "
                   "refusing to drop it without a backup.")
    return int(values[1]) if values[0] != "0" else None


def plan_backup(args: argparse.Namespace, compose: list[str], database: str,
                backup_dir: Path = BACKUP_DIR, now: datetime | None = None) -> Path | None:
    """Where the pre-import backup goes, or None when there is nothing to keep."""
    if args.no_backup:
        warn(f"--no-backup given: `{database}` will be dropped WITHOUT a backup.")
        return None
    tables = database_tables(compose, database)
    if tables is None:
        info(f"`{database}` does not exist yet; nothing to back up.")
        return None
    if tables == 0:
        info(f"`{database}` has no tables; nothing to back up.")
        return None
    stamp = (now or datetime.now(timezone.utc)).strftime("%Y%m%dT%H%M%SZ")
    return backup_dir / f"{database}-pre-snapshot-{stamp}.sql.gz"


def display_path(path: Path) -> str:
    try:
        return str(path.relative_to(ROOT_DIR))
    except ValueError:
        return str(path)


def human_size(size: int) -> str:
    if size < 1024:
        return f"{size} B"
    value = float(size)
    for unit in ("KiB", "MiB", "GiB"):
        value /= 1024
        if value < 1024 or unit == "GiB":
            break
    return f"{value:.1f} {unit}"


def restore_command(compose: list[str], path: Path) -> str:
    return (f"gunzip -c {shell_quote(display_path(path))} | "
            f"{' '.join(compose)} exec -T mariadb sh -c {shell_quote(RESTORE_SHELL)}")


def verify_backup(path: Path) -> None:
    """The gzip is intact and mariadb-dump wrote its completion line."""
    try:
        test = subprocess.run(["gzip", "-t", str(path)], capture_output=True, text=True, check=False)
    except OSError:
        test = None  # no gzip binary; the read below still checks every CRC
    if test is not None and test.returncode != 0:
        raise Fail(f"gzip -t rejected the backup: {test.stderr.strip()[:200]}")
    tail = b""
    try:
        with gzip.open(path, "rb") as gz:
            while chunk := gz.read(1 << 20):
                tail = (tail + chunk)[-4096:]
    except (OSError, EOFError) as exc:
        raise Fail(f"the backup is not a readable gzip file: {exc}") from None
    lines = [line for line in tail.splitlines() if line.strip()]
    if not lines or not lines[-1].startswith(DUMP_COMPLETE):
        raise Fail("the backup does not end with mariadb-dump's '-- Dump completed' line; the dump stopped early.")


def backup_database(compose: list[str], path: Path) -> int:
    """Dump the app database to path (gzipped); returns its size. Raises Fail and leaves nothing behind."""
    directory = path.parent
    if not directory.is_dir():
        directory.mkdir(mode=0o700)
        os.chmod(directory, 0o700)
    if path.exists():
        raise Fail(f"{display_path(path)} already exists; re-run in a second.")

    # mkstemp creates the file 0600. It only takes the real name once verified.
    fd, tmp_name = tempfile.mkstemp(dir=directory, prefix=f".{path.name}.", suffix=".partial")
    tmp = Path(tmp_name)
    proc = None
    try:
        with tempfile.TemporaryFile() as errlog:
            with os.fdopen(fd, "wb") as out:
                with gzip.GzipFile(filename="", mode="wb", fileobj=out) as gz:
                    proc = subprocess.Popen(
                        [*compose, "exec", "-T", "mariadb", "sh", "-c", DUMP_SHELL],
                        cwd=ROOT_DIR,
                        stdin=subprocess.DEVNULL,
                        stdout=subprocess.PIPE,
                        stderr=errlog,
                    )
                    assert proc.stdout is not None
                    while chunk := proc.stdout.read(1 << 20):
                        gz.write(chunk)
                    code = proc.wait()
                out.flush()
                os.fsync(out.fileno())
            errlog.seek(0)
            stderr = errlog.read().decode("utf-8", "replace").strip()
        if code != 0:
            raise Fail(f"mariadb-dump exited {code}: {stderr[:300] or 'no error output'}")
        verify_backup(tmp)
        os.replace(tmp, path)
    except BaseException as exc:
        if proc is not None and proc.poll() is None:
            proc.kill()
            proc.wait()
        tmp.unlink(missing_ok=True)
        if isinstance(exc, Fail):
            raise Fail(f"the backup failed, so nothing was dropped: {exc}") from None
        if isinstance(exc, OSError):
            raise Fail(f"could not write the backup, so nothing was dropped: {exc}") from None
        raise
    finally:
        if proc is not None and proc.stdout is not None:
            proc.stdout.close()
    return path.stat().st_size


# --------------------------------------------------------------------------
# Confirmation
# --------------------------------------------------------------------------


def confirm(args: argparse.Namespace, compose: list[str], manifest: dict, database: str, sql_path: Path,
            backup: Path | None) -> None:
    print("\n" + "=" * 72)
    print("  DESTRUCTIVE: this replaces the local dev database with a snapshot.")
    print("=" * 72)
    print(f"\n  Snapshot: {sql_path.name}")
    print(f"    created {manifest['created_at']}, CMS commit {manifest['source_commit']}, "
          f"scrub v{manifest['scrub_version']}")
    print(f"\n  About to DROP DATABASE `{database}` in the local compose mariadb container")
    print("  and load the snapshot in its place. Everything in it goes:")
    print("    - cms_users and cms_sessions   -> nobody can sign in until you log in again")
    print("    - cms_settings, polls and votes, the media catalogue, anything made by hand")
    print("  The docker volume, other databases in it, and files on disk are kept.")
    if backup is not None:
        print(f"\n  Backup first: the current database is dumped to {display_path(backup)}")
        print("  and the drop only happens once that dump is complete (restore: see README).")
    elif args.no_backup:
        print("\n  NO BACKUP (--no-backup): what is listed below is gone for good.")
    else:
        print("\n  No backup: the database is missing or has no tables.")

    counts = {}
    for table in CONFIRM_TABLES:
        result = mariadb_query(compose, f"SELECT COUNT(*) FROM `{table}`")
        if result is not None and result.isdigit():
            counts[table] = result
    if counts:
        print("\n  Current contents (all of this is destroyed):")
        for table, count in counts.items():
            print(f"    {table:<20} {count:>10}")
    else:
        print("\n  (The database is empty or not queryable, so nothing to report.)")

    print("\n  If you run the backend outside Docker (go run), stop it first.")
    print("  NOT affected: production. This script only talks to the local Docker socket.\n")

    if args.yes:
        info("--yes given; continuing without confirmation.")
        return
    if not sys.stdin.isatty():
        raise Fail("not a terminal and --yes was not given; refusing to destroy data unprompted.")
    reply = input('  Type "import" to continue, anything else to abort: ').strip()
    if reply != "import":
        raise Fail("aborted at the confirmation prompt; nothing was changed.")


# --------------------------------------------------------------------------
# Load
# --------------------------------------------------------------------------


class HashingReader:
    """File wrapper that hashes every byte read, so the load re-checks the sha256."""

    mode = "rb"

    def __init__(self, fh) -> None:
        self._fh = fh
        self._digest = hashlib.sha256()

    def read(self, size: int = -1) -> bytes:
        data = self._fh.read(size)
        self._digest.update(data)
        return data

    def hexdigest(self) -> str:
        while self.read(1 << 20):
            pass
        return self._digest.hexdigest()


def recreate_database(compose: list[str], database: str) -> None:
    db = f"`{database}`"
    root_exec(
        compose,
        f"DROP DATABASE IF EXISTS {db}; "
        f"CREATE DATABASE {db} CHARACTER SET utf8mb4; "
        f"CREATE TABLE {db}.`{MARKER_TABLE}` (reason VARCHAR(255) NOT NULL) ENGINE=InnoDB; "
        f"INSERT INTO {db}.`{MARKER_TABLE}` VALUES ('import started; not verified');",
    )


def load_snapshot(compose: list[str], sql_path: Path, expected_sha: str) -> None:
    with tempfile.TemporaryFile() as errlog, open(sql_path, "rb") as raw:
        source = HashingReader(raw)
        proc = subprocess.Popen(
            [*compose, "exec", "-T", "mariadb", "sh", "-c", IMPORT_SHELL],
            cwd=ROOT_DIR,
            stdin=subprocess.PIPE,
            stdout=subprocess.DEVNULL,
            stderr=errlog,
        )
        assert proc.stdin is not None
        broken_pipe = False
        try:
            with gzip.GzipFile(fileobj=source) as gz:  # type: ignore[arg-type]
                while chunk := gz.read(1 << 20):
                    proc.stdin.write(chunk)
        except BrokenPipeError:
            broken_pipe = True
        except (OSError, EOFError) as exc:
            proc.kill()
            proc.wait()
            raise Fail(f"could not decompress {sql_path.name} during the load: {exc}") from None
        finally:
            try:
                proc.stdin.close()
            except BrokenPipeError:
                broken_pipe = True
        code = proc.wait()
        errlog.seek(0)
        errors = [line for line in errlog.read().decode("utf-8", "replace").splitlines() if line.startswith("ERROR")]
        actual_sha = source.hexdigest()

    if code != 0 or errors or broken_pipe:
        detail = errors[0][:300] if errors else f"mariadb exited {code}"
        raise Fail(f"the load failed: {detail}")
    if actual_sha != expected_sha:
        raise Fail(f"{sql_path.name} changed on disk during the load (sha256 now {actual_sha}).")


# --------------------------------------------------------------------------
# Verification
# --------------------------------------------------------------------------

Query = Callable[[str], "str | None"]


def list_tables(query: Query) -> set[str]:
    result = query("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
    if result is None:
        raise Fail("could not list the tables in the local database.")
    return {line.strip() for line in result.splitlines() if line.strip()}


def verify_anonymization(query: Query, tables: set[str]) -> list[str]:
    """Every way the loaded data breaks the producer's guarantees. Fails closed."""
    problems = []
    for table in MUST_BE_EMPTY:
        if table not in tables:
            continue
        rows = query(f"SELECT COUNT(*) FROM `{table}`")
        if rows != "0":
            problems.append(f"{table} has {rows if rows is not None else 'an unreadable number of'} rows; it must be empty")
    for table, what, sql in ANONYMIZATION_CHECKS:
        if table not in tables:
            problems.append(f"{table} is missing, so {what} cannot be checked")
            continue
        rows = query(sql)
        if rows is None or not rows.isdigit():
            problems.append(f"could not check {what} (query failed)")
        elif rows != "0":
            problems.append(f"{rows} {what}")
    return problems


def verify_counts(query: Query, tables: set[str], expected: Mapping[str, int]) -> tuple[list[str], dict[str, str]]:
    problems, actual = [], {}
    for table, rows in sorted(expected.items()):
        if table not in tables:
            actual[table] = "MISSING"
            problems.append(f"{table} is in the manifest but was not loaded")
            continue
        count = query(f"SELECT COUNT(*) FROM `{table}`")
        actual[table] = count if count is not None else "ERROR"
        if count != str(rows):
            problems.append(f"{table} has {actual[table]} rows, manifest says {rows}")
    return problems, actual


def mark_unusable(compose: list[str], database: str, reason: str) -> None:
    """Leave the marker in place with the reason, and keep the CMS off the bad data."""
    run([*compose, "stop", "cms"], cwd=ROOT_DIR, capture=True, check=False)
    try:
        root_exec(
            compose,
            f"CREATE TABLE IF NOT EXISTS `{database}`.`{MARKER_TABLE}` (reason VARCHAR(255) NOT NULL) ENGINE=InnoDB; "
            f"INSERT INTO `{database}`.`{MARKER_TABLE}` VALUES ({sql_string(reason[:255])});",
        )
    except Fail as exc:
        warn(f"could not record the failure in {MARKER_TABLE}: {exc}")


def fail_unusable(compose: list[str], database: str, problems: list[str], when: str) -> Fail:
    for problem in problems:
        print(f"    - {problem}", file=sys.stderr)
    mark_unusable(compose, database, f"{when}: {problems[0]}")
    return Fail(
        f"the snapshot failed verification {when}. The CMS is stopped and the database is marked "
        f"unusable ({MARKER_TABLE}). Do not use it; re-import a good snapshot or reseed."
    )


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    restore_hint = None
    try:
        try:
            hostname = socket.gethostname()
        except OSError as exc:
            raise Fail(f"could not determine this machine's hostname for the fleet-host check: {exc}") from None
        check_not_fleet_host(hostname, local_ipv4_addresses())

        step("Validating the snapshot")
        sql_path, manifest_path = resolve_paths(args)
        manifest = load_manifest(manifest_path)
        info(f"manifest: {manifest_path.name} (format {manifest['format']}, scrub v{manifest['scrub_version']})")
        check_sha256(sql_path, manifest["sql_sha256"])
        info("sha256 matches")
        statements = scan_snapshot(sql_path)
        info(f"{statements} statements scanned; nothing forbidden")

        step("Preflight")
        compose = resolve_compose()
        check_local_docker(os.environ, docker_context_host)
        preflight()
        database = container_database(compose)
        info(f"target: database `{database}` in the local compose mariadb container")

        backup = plan_backup(args, compose, database)
        confirm(args, compose, manifest, database, sql_path, backup)

        if backup is not None:
            step("Backing up the current database")
            size = backup_database(compose, backup)
            restore_hint = restore_command(compose, backup)
            info(f"backup: {display_path(backup)} ({human_size(size)})")
            info(f"restore: {restore_hint}")

        step("Recreating the database")
        run([*compose, "stop", "cms"], cwd=ROOT_DIR, check=False)
        recreate_database(compose, database)

        step("Loading the snapshot (as the app user)")
        try:
            load_snapshot(compose, sql_path, manifest["sql_sha256"])
        except Fail as exc:
            raise fail_unusable(compose, database, [str(exc)], "during the load") from None

        query: Query = lambda sql: mariadb_query(compose, sql)  # noqa: E731

        step("Verifying anonymization and row counts")
        tables = list_tables(query)
        count_problems, actual = verify_counts(query, tables, manifest["counts"])
        for table, count in actual.items():
            info(f"{table:<24} {count:>10}")
        problems = verify_anonymization(query, tables) + count_problems
        if problems:
            raise fail_unusable(compose, database, problems, "after the load")

        step("Starting the CMS (runs the startup schema migrations)")
        started = run([*compose, "start", "cms"], cwd=ROOT_DIR, check=False).returncode == 0
        healthy = False
        if started:
            healthy = wait_cms_healthy(compose)
        else:
            warn("could not start the cms container; create it with ./scripts/setup_containers.py, then re-run.")

        step("Re-verifying anonymization after migrations")
        problems = verify_anonymization(query, list_tables(query))
        if problems:
            raise fail_unusable(compose, database, problems, "after the CMS migrations")

        root_exec(compose, f"DROP TABLE `{database}`.`{MARKER_TABLE}`")

        if not healthy:
            print(
                "\nThe snapshot is loaded and verified, but the CMS is not healthy, so its startup"
                "\nmigrations may not have run. Check `docker compose logs cms`; the usual causes are an"
                "\nunreachable OIDC_ISSUER_URL in .env (blank it to run read-only) or unreadable"
                "\nserver/certs. Once it is healthy the data is ready; no re-import is needed.",
                file=sys.stderr,
            )
            if restore_hint:
                print(f"  Previous database: {restore_hint}", file=sys.stderr)
            return 3

        print("\nDone. The local database now holds the anonymized snapshot.")
        print("  API:   curl -k https://localhost:8080/v1/health/db")
        print("  Login: cms_users is empty, so the first account to sign in through OIDC becomes admin.")
        print("         Without OIDC_* in .env the CMS runs read-only (AUTH DISABLED). See README.")
        print("  Media: images are not included; see README for the read-only fallback.")
        if restore_hint:
            print(f"  Previous database: {restore_hint}")
        return 0

    except Fail as exc:
        print(f"\nERROR: {exc}", file=sys.stderr)
        if restore_hint:
            print(f"  To put the previous database back: {restore_hint}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("\nInterrupted.", file=sys.stderr)
        if restore_hint:
            print(f"  To put the previous database back: {restore_hint}", file=sys.stderr)
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
