"""Unit tests for scripts/import_snapshot.py. No Docker, no real data.

  python -m unittest discover -s scripts/tests
"""

from __future__ import annotations

import gzip
import hashlib
import io
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import import_snapshot as snap  # noqa: E402
from reseed_from_etl import Fail  # noqa: E402

# Shaped like real mariadb-dump 11 output, with data chosen to look like the
# statements the scanner refuses, so a clean pass proves it skips literals.
CLEAN_SQL = r"""/*M!999999\- enable the sandbox mode */
-- MariaDB dump 10.19-11.7.2-MariaDB, for debian-linux-gnu (x86_64)
--
-- Host: localhost    Database: triangle
-- ------------------------------------------------------
/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*M!100616 SET @OLD_NOTE_VERBOSITY=@@NOTE_VERBOSITY, NOTE_VERBOSITY=0 */;

--
-- Table structure for table `authors`
--

DROP TABLE IF EXISTS `authors`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `authors` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `display_name` longtext DEFAULT NULL,
  `login` varchar(255) DEFAULT NULL,
  `email` varchar(255) DEFAULT NULL,
  `grant` int(11) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

LOCK TABLES `authors` WRITE;
/*!40000 ALTER TABLE `authors` DISABLE KEYS */;
INSERT INTO `authors` VALUES (1,'Jane Doe','author-1',NULL,0),(2,'O\'Brien; USE mysql; GRANT ALL ON *.* TO x','author-2',NULL,0);
/*!40000 ALTER TABLE `authors` ENABLE KEYS */;
UNLOCK TABLES;

DROP TABLE IF EXISTS `articles`;
CREATE TABLE `articles` (
  `id` bigint(20) NOT NULL,
  `title` longtext DEFAULT NULL,
  `text` longtext DEFAULT NULL,
  `pub_date` datetime DEFAULT NULL,
  `archived_at` datetime DEFAULT NULL,
  `author_id` bigint(20) DEFAULT NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `fk_author` FOREIGN KEY (`author_id`) REFERENCES `authors` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO `articles` VALUES (0,'Zero','\\! rm -rf / -- not a comment # nor this /* nor this','2013-05-17 12:06:00',NULL,1),(1,"it's \"quoted\"",'see mysql.user and LOAD DATA INFILE; DEFINER=`root`@`%`','2018-12-07 12:39:36',NULL,2);
INSERT INTO `articles` VALUES (2,'Multi-line','line one
DROP DATABASE triangle;
source /etc/passwd
line four','2020-01-01 00:00:00',NULL,NULL);
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET @saved_cs_results     = @@character_set_results */ ;
/*!50003 SET @saved_col_connection = @@collation_connection */ ;
/*!50003 SET character_set_client  = utf8mb3 */ ;
/*!50003 SET character_set_results = utf8mb3 */ ;
/*!50003 SET collation_connection  = utf8mb3_general_ci */ ;
/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;
/*!50003 SET sql_mode              = 'STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=CURRENT_USER*/ /*!50003 TRIGGER `articles_bi` BEFORE INSERT ON `articles` FOR EACH ROW SET NEW.title = TRIM(NEW.title) */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
/*!50003 SET character_set_results = @saved_cs_results */ ;
/*!50003 SET collation_connection  = @saved_col_connection */ ;
SET @trailing = 'ends in a backslash\\';
/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*M!100616 SET NOTE_VERBOSITY=@OLD_NOTE_VERBOSITY */;

-- Dump completed on 2026-10-01  4:00:00
"""

CLEAN_COUNTS = {"authors": 2, "articles": 3, "cms_users": 0, "comments": 0}


def make_snapshot(directory: Path, sql: str = CLEAN_SQL, ts: str = "20261001T040000Z", **overrides) -> Path:
    """Write a tiny triangle-snapshot-<ts>.sql.gz and matching manifest; return the .sql.gz path."""
    sql_path = directory / f"triangle-snapshot-{ts}.sql.gz"
    sql_path.write_bytes(gzip.compress(sql.encode("utf-8")))
    manifest = {
        "format": 1,
        "anonymized": True,
        "created_at": "2026-10-01T04:00:00Z",
        "source_commit": "8662fd1a" * 5,
        "scrub_version": 1,
        "sql_sha256": hashlib.sha256(sql_path.read_bytes()).hexdigest(),
        "counts": dict(CLEAN_COUNTS),
    }
    manifest.update(overrides)
    manifest = {k: v for k, v in manifest.items() if v is not DROP}
    (directory / f"triangle-snapshot-{ts}.manifest.json").write_text(json.dumps(manifest))
    return sql_path


DROP = object()


def kinds(sql: str) -> set[str]:
    findings, _ = snap.scan_sql(io.StringIO(sql))
    return {f.kind for f in findings}


class ScannerTest(unittest.TestCase):
    def test_clean_dump_passes(self):
        findings, statements = snap.scan_sql(io.StringIO(CLEAN_SQL))
        self.assertEqual(findings, [])
        self.assertGreater(statements, 15)

    def assertRefused(self, kind: str, *statements: str):
        for statement in statements:
            with self.subTest(statement=statement):
                self.assertIn(kind, kinds(CLEAN_SQL + statement))

    def test_cross_database_writes(self):
        self.assertRefused(
            "cross-database name",
            "INSERT INTO `mysql`.`user` VALUES (1);\n",
            "INSERT INTO other.articles VALUES (1);\n",
            "REPLACE INTO `triangle` . `articles` VALUES (1);\n",
            "UPDATE `triangle`.`articles` SET title='x';\n",
            "DELETE FROM otherdb.comments;\n",
            "DROP TABLE IF EXISTS `a`, `otherdb`.`b`;\n",
            "CREATE TABLE x LIKE other.y;\n",
            "ALTER TABLE other.y ADD COLUMN z INT;\n",
            "RENAME TABLE `authors` TO `other`.`authors`;\n",
            "CREATE TRIGGER t BEFORE INSERT ON other.articles FOR EACH ROW SET @x = 1;\n",
            "SELECT * FROM information_schema.tables;\n",
        )

    def test_database_statements(self):
        self.assertRefused(
            "database statement",
            "CREATE DATABASE evil;\n",
            "DROP SCHEMA IF EXISTS triangle;\n",
            "/*!40000 CREATE DATABASE IF NOT EXISTS evil */;\n",
            "ALTER DATABASE triangle CHARACTER SET latin1;\n",
        )

    def test_use(self):
        self.assertIn("client command", kinds(CLEAN_SQL + "USE mysql;\n"))
        self.assertRefused("USE statement", "SELECT 1; USE mysql;\n", "/*!40000 USE mysql */;\n")

    def test_privilege_statements(self):
        self.assertRefused(
            "privilege statement",
            "GRANT ALL ON *.* TO 'x'@'%';\n",
            "/*!50000 GRANT SUPER ON *.* TO evil */;\n",
            "REVOKE ALL PRIVILEGES ON t FROM y;\n",
            "CREATE USER 'x'@'%' IDENTIFIED BY 'y';\n",
            "DROP USER 'triangle_user'@'%';\n",
            "SET PASSWORD FOR root = PASSWORD('x');\n",
            "CREATE ROLE admin;\n",
        )

    def test_client_commands(self):
        self.assertRefused(
            "client command",
            "\\! rm -rf /\n",
            "SELECT 1 \\! id\n;\n",
            "\\. /tmp/other.sql\n",
            "source /tmp/other.sql\n",
            "SOURCE /tmp/other.sql\n",
            "system id\n",
            "connect otherdb\n",
            "pager cat > /tmp/x\n",
            "tee /tmp/x\n",
        )

    def test_file_load_and_export(self):
        self.assertRefused(
            "file load/export",
            "LOAD DATA INFILE '/etc/passwd' INTO TABLE authors;\n",
            "LOAD DATA LOCAL INFILE 'x.csv' INTO TABLE authors;\n",
            "LOAD XML LOCAL INFILE 'x.xml' INTO TABLE authors;\n",
            "SELECT * FROM authors INTO OUTFILE '/tmp/x';\n",
            "INSERT INTO authors VALUES (9, LOAD_FILE('/etc/passwd'), 'author-9', NULL, 0);\n",
        )

    def test_definers(self):
        self.assertRefused(
            "DEFINER other than CURRENT_USER",
            "/*!50001 CREATE ALGORITHM=UNDEFINED */ /*!50013 DEFINER=`root`@`localhost` SQL SECURITY DEFINER */ "
            "/*!50001 VIEW `v` AS select 1 AS `x` */;\n",
            "CREATE DEFINER='root'@'%' TRIGGER t BEFORE INSERT ON authors FOR EACH ROW SET @x = 1;\n",
            "CREATE DEFINER=root@localhost PROCEDURE p() SELECT 1;\n",
        )

    def test_definer_current_user_allowed(self):
        self.assertEqual(
            kinds("CREATE DEFINER=CURRENT_USER() TRIGGER t BEFORE INSERT ON authors FOR EACH ROW SET NEW.id = NEW.id;\n"),
            set(),
        )

    def test_server_level_statements(self):
        self.assertRefused(
            "server-level statement",
            "SET GLOBAL general_log = 1;\n",
            "SET @@GLOBAL.read_only = 0;\n",
            "INSTALL SONAME 'evil';\n",
        )

    def test_dynamic_sql_and_routines(self):
        self.assertRefused(
            "dynamic SQL or stored routine",
            "SET @s = 'GRANT ALL ON *.* TO x'; PREPARE s FROM @s; EXECUTE s;\n",
            "EXECUTE IMMEDIATE 'DROP DATABASE triangle';\n",
            "CALL cleanup();\n",
            "CREATE PROCEDURE p() SELECT 1;\n",
            "/*!50106 CREATE*/ /*!50117 DEFINER=CURRENT_USER*/ /*!50106 EVENT `e` ON SCHEDULE EVERY 1 DAY DO DELETE FROM authors */;\n",
        )

    def test_tokenizer_changing_settings(self):
        # Each would make the server split the rest of the dump differently.
        self.assertRefused(
            "sql_mode or charset change",
            "SET sql_mode = 'ANSI_QUOTES';\n",
            "SET SESSION sql_mode = 'STRICT_TRANS_TABLES,NO_BACKSLASH_ESCAPES';\n",
            "/*!40101 SET SQL_MODE='ANSI' */;\n",
            "SET sql_mode = 'ANSI_' 'QUOTES';\n",
            "SET sql_mode = CONCAT('ANSI_', 'QUOTES');\n",
            "SET sql_mode = 4;\n",
            "SET sql_mode = 0x414E53495F51554F544553;\n",
            "SET @x = 'ANSI_QUOTES'; SET sql_mode = @x;\n",
            "SET @OLD_SQL_MODE = 'ANSI_QUOTES';\n",
            "SET @@session.sql_mode := 'ORACLE';\n",
            "SET STATEMENT sql_mode='NO_BACKSLASH_ESCAPES' FOR SELECT 1;\n",
            "SET NAMES gbk;\n",
            "SET NAMES 'sjis';\n",
            "SET CHARACTER SET big5;\n",
            "SET character_set_client = gbk;\n",
        )

    def test_unterminated_literal(self):
        self.assertRefused("unterminated string or comment", "INSERT INTO authors VALUES ('abc);\n")

    def test_trailing_statement_without_delimiter_is_checked(self):
        self.assertIn("privilege statement", kinds(CLEAN_SQL + "GRANT ALL ON *.* TO x"))


class ManifestTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)

    def tearDown(self):
        self.tmp.cleanup()

    def manifest_for(self, **overrides) -> Path:
        sql = make_snapshot(self.dir, **overrides)
        return sql.with_name(sql.name.replace(".sql.gz", ".manifest.json"))

    def test_valid_manifest(self):
        manifest = snap.load_manifest(self.manifest_for())
        self.assertEqual(manifest["format"], 1)

    def test_rejects_bad_fields(self):
        cases = {
            "format 2": {"format": 2},
            "format as string": {"format": "1"},
            "format as bool": {"format": True},
            "not anonymized": {"anonymized": False},
            "anonymized missing": {"anonymized": DROP},
            "anonymized truthy string": {"anonymized": "true"},
            "bad created_at": {"created_at": "yesterday"},
            "bad source_commit": {"source_commit": "main"},
            "zero scrub_version": {"scrub_version": 0},
            "bad sha": {"sql_sha256": "abc"},
            "counts missing": {"counts": DROP},
            "negative count": {"counts": {"articles": -1}},
            "bool count": {"counts": {"articles": True}},
            "bad table name": {"counts": {"articles; DROP": 1}},
            "users present": {"counts": {"articles": 3, "cms_users": 2}},
            "classifieds present": {"counts": {"articles": 3, "classifieds": 1}},
        }
        for name, overrides in cases.items():
            with self.subTest(name), self.assertRaises(Fail):
                snap.load_manifest(self.manifest_for(**overrides))

    def test_not_json(self):
        path = self.dir / "x.manifest.json"
        path.write_text("{nope")
        with self.assertRaises(Fail):
            snap.load_manifest(path)

    def test_sha_mismatch(self):
        sql = make_snapshot(self.dir, sql_sha256="0" * 64)
        manifest = snap.load_manifest(sql.with_name(sql.name.replace(".sql.gz", ".manifest.json")))
        with self.assertRaisesRegex(Fail, "sha256"):
            snap.check_sha256(sql, manifest["sql_sha256"])

    def test_sha_match(self):
        sql = make_snapshot(self.dir)
        manifest = snap.load_manifest(sql.with_name(sql.name.replace(".sql.gz", ".manifest.json")))
        snap.check_sha256(sql, manifest["sql_sha256"])

    def test_resolve_paths(self):
        sql = make_snapshot(self.dir)
        manifest = sql.with_name(sql.name.replace(".sql.gz", ".manifest.json"))
        self.assertEqual(snap.resolve_paths(snap.parse_args([str(sql)])), (sql.resolve(), manifest.resolve()))
        self.assertEqual(snap.resolve_paths(snap.parse_args(["--manifest", str(manifest)])), (sql.resolve(), manifest.resolve()))
        manifest.unlink()
        with self.assertRaises(Fail):
            snap.resolve_paths(snap.parse_args([str(sql)]))


def dev_machine(hostname: str = "dev-laptop", addresses: list[str] | None = None):
    """Patchers that make main() see an ordinary contributor machine."""
    return (
        mock.patch.object(snap.socket, "gethostname", return_value=hostname),
        mock.patch.object(snap, "local_ipv4_addresses",
                          return_value=addresses if addresses is not None else ["127.0.0.1", "192.168.1.20"]),
    )


class MainValidationTest(unittest.TestCase):
    """Refusals must happen before Docker is touched at all."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        patcher = mock.patch.object(snap, "resolve_compose", side_effect=AssertionError("docker touched"))
        self.compose = patcher.start()
        self.addCleanup(patcher.stop)
        for patcher in dev_machine():
            patcher.start()
            self.addCleanup(patcher.stop)

    def tearDown(self):
        self.tmp.cleanup()

    def run_main(self, sql_path: Path) -> int:
        with mock.patch("sys.stdout", io.StringIO()), mock.patch("sys.stderr", io.StringIO()):
            return snap.main([str(sql_path), "--yes"])

    def test_forbidden_statement_refused_before_docker(self):
        self.assertEqual(self.run_main(make_snapshot(self.dir, sql=CLEAN_SQL + "GRANT ALL ON *.* TO x;\n")), 1)
        self.compose.assert_not_called()

    def test_sha_mismatch_refused_before_docker(self):
        self.assertEqual(self.run_main(make_snapshot(self.dir, sql_sha256="f" * 64)), 1)
        self.compose.assert_not_called()

    def test_corrupt_gzip_refused_before_docker(self):
        sql = make_snapshot(self.dir)
        sql.write_bytes(b"not gzip")
        manifest = sql.with_name(sql.name.replace(".sql.gz", ".manifest.json"))
        data = json.loads(manifest.read_text())
        data["sql_sha256"] = hashlib.sha256(b"not gzip").hexdigest()
        manifest.write_text(json.dumps(data))
        self.assertEqual(self.run_main(sql), 1)
        self.compose.assert_not_called()


class DockerTest(unittest.TestCase):
    LOCAL = "unix:///var/run/docker.sock"

    def test_local_socket_allowed(self):
        snap.check_local_docker({}, lambda: self.LOCAL)
        snap.check_local_docker({"DOCKER_HOST": "unix:///run/user/1000/docker.sock"}, lambda: "unix:///run/user/1000/docker.sock")

    def test_remote_docker_host_refused(self):
        for host in ("tcp://10.248.40.168:2375", "ssh://tadmin@delta", "tcp://127.0.0.1:2376"):
            with self.subTest(host), self.assertRaisesRegex(Fail, "DOCKER_HOST"):
                snap.check_local_docker({"DOCKER_HOST": host}, lambda: self.LOCAL)

    def test_remote_context_refused(self):
        with self.assertRaisesRegex(Fail, "delta"):
            snap.check_local_docker({"DOCKER_CONTEXT": "delta"}, lambda: "ssh://tadmin@delta")
        with self.assertRaises(Fail):
            snap.check_local_docker({"DOCKER_HOST": self.LOCAL}, lambda: "tcp://remote:2376")

    def test_unknown_context_refused(self):
        with self.assertRaises(Fail):
            snap.check_local_docker({}, lambda: None)


def fake_query(results: dict[str, str | None]):
    def query(sql: str) -> str | None:
        for needle, result in results.items():
            if needle in sql:
                return result
        return "0"
    return query


ALL_TABLES = {"authors", "articles", "comments", "cms_users", "cms_sessions", "classifieds", "cms_activity"}


class VerificationTest(unittest.TestCase):
    def test_clean(self):
        self.assertEqual(snap.verify_anonymization(fake_query({}), ALL_TABLES), [])

    def test_failures(self):
        cases = {
            "users present": {"FROM `cms_users`": "3"},
            "sessions present": {"FROM `cms_sessions`": "1"},
            "activity present": {"FROM `cms_activity`": "7"},
            "classifieds present": {"FROM `classifieds`": "2"},
            "author email": {"FROM `authors`": "2"},
            "comment pii": {"FROM `comments`": "5"},
            "unpublished article": {"FROM `articles`": "1"},
            "query error": {"FROM `comments`": None},
            "count unreadable": {"FROM `cms_users`": None},
            "garbage result": {"FROM `articles`": "ERROR 1054"},
        }
        for name, results in cases.items():
            with self.subTest(name):
                self.assertNotEqual(snap.verify_anonymization(fake_query(results), ALL_TABLES), [])

    def test_missing_checked_table_fails(self):
        self.assertNotEqual(snap.verify_anonymization(fake_query({}), ALL_TABLES - {"comments"}), [])

    def test_missing_must_be_empty_table_is_fine(self):
        self.assertEqual(snap.verify_anonymization(fake_query({}), ALL_TABLES - {"cms_activity"}), [])

    def test_counts(self):
        query = fake_query({"FROM `articles`": "3", "FROM `authors`": "2"})
        problems, _ = snap.verify_counts(query, ALL_TABLES, {"articles": 3, "authors": 2})
        self.assertEqual(problems, [])
        problems, _ = snap.verify_counts(query, ALL_TABLES, {"articles": 4})
        self.assertEqual(len(problems), 1)
        problems, actual = snap.verify_counts(query, ALL_TABLES, {"polls": 1})
        self.assertEqual(actual["polls"], "MISSING")
        self.assertEqual(len(problems), 1)

    def test_failure_marks_database_unusable(self):
        with mock.patch.object(snap, "run") as run, mock.patch.object(snap, "root_exec") as root_exec, \
                mock.patch("sys.stderr", io.StringIO()):
            error = snap.fail_unusable(["docker", "compose"], "triangle", ["cms_users has 3 rows"], "after the load")
        self.assertIsInstance(error, Fail)
        run.assert_called_once()
        self.assertEqual(run.call_args.args[0], ["docker", "compose", "stop", "cms"])
        sql = root_exec.call_args.args[1]
        self.assertIn(snap.MARKER_TABLE, sql)
        self.assertIn("cms_users has 3 rows", sql)


class ConfirmTest(unittest.TestCase):
    MANIFEST = {"created_at": "t", "source_commit": "abc1234", "scrub_version": 1}

    def confirm(self, argv: list[str], backup: Path | None) -> str:
        """Run confirm without a TTY (so it must refuse unless --yes); return what it printed."""
        out = io.StringIO()
        with mock.patch.object(snap, "mariadb_query", return_value="5"), \
                mock.patch("sys.stdin.isatty", return_value=False), mock.patch("sys.stdout", out):
            try:
                snap.confirm(snap.parse_args(argv), [], self.MANIFEST, "triangle", Path("x.sql.gz"), backup)
            except Fail as exc:
                out.write(f"\nFAIL: {exc}")
        return out.getvalue()

    def test_refuses_without_tty(self):
        self.assertIn("FAIL: not a terminal", self.confirm(["x.sql.gz"], None))

    def test_names_backup_path(self):
        backup = snap.BACKUP_DIR / "triangle-pre-snapshot-20261006T120000Z.sql.gz"
        text = self.confirm(["x.sql.gz"], backup)
        self.assertIn("db-backups/triangle-pre-snapshot-20261006T120000Z.sql.gz", text)
        self.assertIn("FAIL: not a terminal", text)

    def test_no_backup_still_needs_confirmation(self):
        text = self.confirm(["x.sql.gz", "--no-backup"], None)
        self.assertIn("NO BACKUP", text)
        self.assertIn("FAIL: not a terminal", text)


class FleetGuardTest(unittest.TestCase):
    def test_fleet_hostnames_refused(self):
        for host in ("thetriangle-delta", "THETRIANGLE-DB1-LXC", "thetriangle-maxscale",
                     "thetriangle-wordpress.drexel.edu"):
            with self.subTest(host), self.assertRaisesRegex(Fail, "fleet host"):
                snap.check_not_fleet_host(host, ["192.168.1.20"])

    def test_hostname_checked_even_without_addresses(self):
        with mock.patch("sys.stderr", io.StringIO()), self.assertRaisesRegex(Fail, "fleet host"):
            snap.check_not_fleet_host("thetriangle-delta", None)

    def test_fleet_addresses_refused(self):
        for address in ("10.248.40.168", "10.248.40.0", "10.248.40.255", "10.248.41.7", "10.248.42.122"):
            with self.subTest(address), self.assertRaisesRegex(Fail, "fleet network"):
                snap.check_not_fleet_host("dev-laptop", ["127.0.0.1", address])

    def test_other_addresses_pass(self):
        snap.check_not_fleet_host(
            "dev-laptop", ["127.0.0.1", "10.248.39.255", "10.248.42.5", "10.248.43.1", "192.168.1.20", "999.1.1.1"]
        )
        snap.check_not_fleet_host("delta-dev", [])

    def test_tool_missing_warns_and_continues(self):
        self.assertIsNone(snap.local_ipv4_addresses(lambda cmd: None))
        err = io.StringIO()
        with mock.patch("sys.stderr", err):
            snap.check_not_fleet_host("dev-laptop", None)
        self.assertIn("WARNING", err.getvalue())

    def test_parses_ip(self):
        ip_out = ("1: lo    inet 127.0.0.1/8 scope host lo\\       valid_lft forever preferred_lft forever\n"
                  "2: eth0    inet 10.248.40.168/24 brd 10.248.40.255 scope global eth0\\       valid_lft forever\n")
        seen = []

        def output(cmd):
            seen.append(cmd[0])
            return ip_out if cmd[0] == "ip" else None
        self.assertEqual(snap.local_ipv4_addresses(output), ["127.0.0.1", "10.248.40.168"])
        self.assertEqual(seen, ["ip"])

    def test_falls_back_to_ifconfig(self):
        ifconfig_out = ("en0: flags=8863<UP,BROADCAST>\n\tinet 10.248.41.9 netmask 0xffffff00 broadcast 10.248.41.255\n"
                        "eth1      Link encap:Ethernet\n          inet addr:192.168.5.5  Bcast:192.168.5.255\n")
        addresses = snap.local_ipv4_addresses(lambda cmd: ifconfig_out if cmd[0] == "ifconfig" else None)
        self.assertEqual(addresses, ["10.248.41.9", "192.168.5.5"])

    def test_main_refuses_before_validation_and_docker(self):
        patchers = dev_machine(addresses=["10.248.40.168"])
        with patchers[0], patchers[1], \
                mock.patch.object(snap, "resolve_paths", side_effect=AssertionError("validated")) as paths, \
                mock.patch.object(snap, "resolve_compose", side_effect=AssertionError("docker touched")) as compose, \
                mock.patch("sys.stdout", io.StringIO()), mock.patch("sys.stderr", io.StringIO()) as err:
            self.assertEqual(snap.main(["x.sql.gz", "--yes"]), 1)
        paths.assert_not_called()
        compose.assert_not_called()
        self.assertIn("fleet network", err.getvalue())


class PlanBackupTest(unittest.TestCase):
    NOW = snap.datetime(2026, 10, 6, 12, 0, 0, tzinfo=snap.timezone.utc)

    def plan(self, argv: list[str], root_output: str) -> Path | None:
        with mock.patch.object(snap, "root_query", return_value=root_output) as query, \
                mock.patch("sys.stdout", io.StringIO()), mock.patch("sys.stderr", io.StringIO()):
            path = snap.plan_backup(snap.parse_args(argv), [], "triangle", Path("/b"), self.NOW)
        self.query = query
        return path

    def test_backs_up_a_database_with_tables(self):
        self.assertEqual(self.plan(["x.sql.gz"], "1\n12"), Path("/b/triangle-pre-snapshot-20261006T120000Z.sql.gz"))

    def test_skips_missing_and_empty(self):
        self.assertIsNone(self.plan(["x.sql.gz"], "0\n0"))
        self.assertIsNone(self.plan(["x.sql.gz"], "1\n0"))

    def test_unreadable_state_refuses(self):
        for output in ("", "1", "ERROR", "1\nx"):
            with self.subTest(output), self.assertRaisesRegex(Fail, "without a backup"):
                self.plan(["x.sql.gz"], output)

    def test_no_backup_does_not_query(self):
        self.assertIsNone(self.plan(["x.sql.gz", "--no-backup"], "1\n12"))
        self.query.assert_not_called()


# Stand-ins for `docker compose`: backup_database appends `exec -T mariadb sh -c ...`,
# which `python -c` takes as ignored argv.
def fake_dump(body: str, code: int = 0, stderr: str = "") -> list[str]:
    return [sys.executable, "-c",
            f"import sys; sys.stdout.write({body!r}); sys.stderr.write({stderr!r}); sys.exit({code})"]


GOOD_DUMP = "-- MariaDB dump\nCREATE TABLE `authors` (id int);\n\n-- Dump completed on 2026-10-06 12:00:00\n"


class BackupDatabaseTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name) / "db-backups"
        self.path = self.dir / "triangle-pre-snapshot-20261006T120000Z.sql.gz"

    def tearDown(self):
        self.tmp.cleanup()

    def leftovers(self) -> list[str]:
        return sorted(p.name for p in self.dir.iterdir()) if self.dir.exists() else []

    def test_writes_verified_private_backup(self):
        size = snap.backup_database(fake_dump(GOOD_DUMP), self.path)
        self.assertEqual(gzip.decompress(self.path.read_bytes()).decode(), GOOD_DUMP)
        self.assertEqual(size, self.path.stat().st_size)
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.dir.stat().st_mode & 0o777, 0o700)
        self.assertEqual(self.leftovers(), [self.path.name])

    def test_incomplete_dump_refused(self):
        with self.assertRaisesRegex(Fail, "nothing was dropped.*Dump completed"):
            snap.backup_database(fake_dump("CREATE TABLE `authors` (id int);\nINSERT INTO `auth"), self.path)
        self.assertEqual(self.leftovers(), [])

    def test_dump_error_refused(self):
        with self.assertRaisesRegex(Fail, "nothing was dropped.*Access denied"):
            snap.backup_database(fake_dump(GOOD_DUMP, 2, "mariadb-dump: Got error: 1045: Access denied"), self.path)
        self.assertEqual(self.leftovers(), [])

    def test_renames_only_after_verification(self):
        events = []
        real_verify, real_replace = snap.verify_backup, os.replace

        def verify(path):
            events.append(("verify", path.name.endswith(".partial"), self.path.exists()))
            real_verify(path)

        def replace(src, dst):
            events.append(("replace",))
            real_replace(src, dst)
        with mock.patch.object(snap, "verify_backup", side_effect=verify), \
                mock.patch.object(snap.os, "replace", side_effect=replace):
            snap.backup_database(fake_dump(GOOD_DUMP), self.path)
        self.assertEqual(events, [("verify", True, False), ("replace",)])

    def test_failed_verification_never_renames(self):
        with mock.patch.object(snap, "verify_backup", side_effect=Fail("gzip -t rejected the backup")), \
                mock.patch.object(snap.os, "replace") as replace, self.assertRaises(Fail):
            snap.backup_database(fake_dump(GOOD_DUMP), self.path)
        replace.assert_not_called()
        self.assertEqual(self.leftovers(), [])

    def test_verify_rejects_truncated_gzip(self):
        self.dir.mkdir()
        bad = self.dir / "x.sql.gz"
        bad.write_bytes(gzip.compress(GOOD_DUMP.encode())[:-12])
        with self.assertRaisesRegex(Fail, "gzip"):
            snap.verify_backup(bad)

    def test_existing_backup_not_overwritten(self):
        self.dir.mkdir()
        self.path.write_bytes(b"keep")
        with self.assertRaisesRegex(Fail, "already exists"):
            snap.backup_database(fake_dump(GOOD_DUMP), self.path)
        self.assertEqual(self.path.read_bytes(), b"keep")


class _MainHarness(unittest.TestCase):
    """main() from confirmation to the drop, with Docker replaced by mocks."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.sql = make_snapshot(Path(self.tmp.name))
        self.calls = mock.Mock()
        patches = [
            *dev_machine(),
            mock.patch.object(snap, "resolve_compose", return_value=["docker", "compose"]),
            mock.patch.object(snap, "check_local_docker"),
            mock.patch.object(snap, "preflight"),
            mock.patch.object(snap, "container_database", return_value="triangle"),
            mock.patch("sys.stdout", io.StringIO()),
        ]
        for name in ("database_tables", "confirm", "backup_database", "run", "recreate_database", "root_exec"):
            patches.append(mock.patch.object(snap, name, getattr(self.calls, name)))
        for patcher in patches:
            patcher.start()
            self.addCleanup(patcher.stop)
        self.stderr = io.StringIO()
        stderr = mock.patch("sys.stderr", self.stderr)
        stderr.start()
        self.addCleanup(stderr.stop)
        self.calls.database_tables.return_value = 7
        self.calls.backup_database.return_value = 4096
        # Stop main right after the drop; the load is covered elsewhere.
        self.calls.recreate_database.side_effect = Fail("stop after recreate")

    def run_main(self, *extra: str) -> int:
        return snap.main([str(self.sql), "--yes", *extra])

    def names(self) -> list[str]:
        return [call[0] for call in self.calls.mock_calls]


class MainBackupOrderTest(_MainHarness):
    def test_backup_before_drop(self):
        self.assertEqual(self.run_main(), 1)
        self.assertEqual(self.names(), ["database_tables", "confirm", "backup_database", "run", "recreate_database"])
        backup = self.calls.backup_database.call_args.args[1]
        self.assertEqual(self.calls.confirm.call_args.args[5], backup)
        self.assertRegex(backup.name, r"^triangle-pre-snapshot-\d{8}T\d{6}Z\.sql\.gz$")
        self.assertEqual(backup.parent, snap.BACKUP_DIR)
        self.assertIn("To put the previous database back: gunzip -c", self.stderr.getvalue())

    def test_backup_failure_leaves_database_untouched(self):
        self.calls.backup_database.side_effect = Fail("the backup failed, so nothing was dropped: boom")
        self.assertEqual(self.run_main(), 1)
        self.assertEqual(self.names(), ["database_tables", "confirm", "backup_database"])
        self.assertIn("nothing was dropped", self.stderr.getvalue())

    def test_empty_or_missing_database_skips_backup(self):
        for tables in (0, None):
            with self.subTest(tables=tables):
                self.calls.reset_mock()
                self.calls.database_tables.return_value = tables
                self.assertEqual(self.run_main(), 1)
                self.assertEqual(self.names(), ["database_tables", "confirm", "run", "recreate_database"])
                self.assertIsNone(self.calls.confirm.call_args.args[5])

    def test_no_backup_flag(self):
        self.assertEqual(self.run_main("--no-backup"), 1)
        self.assertEqual(self.names(), ["confirm", "run", "recreate_database"])
        self.assertIsNone(self.calls.confirm.call_args.args[5])
        self.assertIn("WITHOUT a backup", self.stderr.getvalue())


class MainCmsHealthTest(_MainHarness):
    """The tail of main(): a verified load with an unhealthy CMS must not report success."""

    wait_cms_healthy: mock.Mock
    load_snapshot: mock.Mock

    def setUp(self):
        super().setUp()
        self.calls.recreate_database.side_effect = None
        self.calls.run.return_value = mock.Mock(returncode=0)
        for name, value in (
            ("load_snapshot", None),
            ("list_tables", ["articles"]),
            ("verify_counts", ([], {"articles": 1})),
            ("verify_anonymization", []),
            ("wait_cms_healthy", True),
        ):
            patcher = mock.patch.object(snap, name, return_value=value)
            setattr(self, name, patcher.start())
            self.addCleanup(patcher.stop)

    def test_healthy_cms_succeeds(self):
        self.assertEqual(self.run_main(), 0)
        self.assertIn("_snapshot_import_unverified", self.calls.root_exec.call_args.args[1])

    def test_unhealthy_cms_is_not_success(self):
        self.wait_cms_healthy.return_value = False
        self.assertEqual(self.run_main(), 3)
        # Data was verified, so the marker is still dropped; the failure is the CMS.
        self.assertIn("_snapshot_import_unverified", self.calls.root_exec.call_args.args[1])
        self.assertIn("CMS is not healthy", self.stderr.getvalue())

    def test_cms_that_cannot_start_is_not_success(self):
        self.calls.run.return_value = mock.Mock(returncode=1)
        self.assertEqual(self.run_main(), 3)
        self.wait_cms_healthy.assert_not_called()

    def test_interrupt_after_backup_prints_restore(self):
        self.load_snapshot.side_effect = KeyboardInterrupt
        self.assertEqual(self.run_main(), 130)
        self.assertIn("To put the previous database back: gunzip -c", self.stderr.getvalue())

    def test_restore_command_allows_large_rows(self):
        self.assertIn("--max-allowed-packet=1G", snap.RESTORE_SHELL)

    def test_hostname_failure_is_a_clean_refusal(self):
        with mock.patch.object(snap.socket, "gethostname", side_effect=OSError("no uts")):
            self.assertEqual(self.run_main(), 1)
        self.assertIn("could not determine this machine's hostname", self.stderr.getvalue())
        self.calls.confirm.assert_not_called()


class LoadTest(unittest.TestCase):
    """load_snapshot against a stand-in client process instead of docker."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.sql = make_snapshot(Path(self.tmp.name))
        self.sha = hashlib.sha256(self.sql.read_bytes()).hexdigest()

    def tearDown(self):
        self.tmp.cleanup()

    def test_streams_and_rechecks_sha(self):
        client = [sys.executable, "-c", "import sys; sys.stdin.buffer.read()"]
        snap.load_snapshot(client, self.sql, self.sha)
        with self.assertRaisesRegex(Fail, "changed on disk"):
            snap.load_snapshot(client, self.sql, "0" * 64)

    def test_client_error_fails(self):
        client = [sys.executable, "-c",
                  "import sys; sys.stderr.write('ERROR 1064 (42000) at line 3: syntax\\n'); sys.exit(1)"]
        with self.assertRaisesRegex(Fail, "ERROR 1064"):
            snap.load_snapshot(client, self.sql, self.sha)


if __name__ == "__main__":
    unittest.main()
