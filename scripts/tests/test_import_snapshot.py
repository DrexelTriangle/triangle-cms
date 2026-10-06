"""Unit tests for scripts/import_snapshot.py. No Docker, no real data.

  python -m unittest discover -s scripts/tests
"""

from __future__ import annotations

import gzip
import hashlib
import io
import json
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


class MainValidationTest(unittest.TestCase):
    """Refusals must happen before Docker is touched at all."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        patcher = mock.patch.object(snap, "resolve_compose", side_effect=AssertionError("docker touched"))
        self.compose = patcher.start()
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
    def test_refuses_without_tty(self):
        args = snap.parse_args(["x.sql.gz"])
        manifest = {"created_at": "t", "source_commit": "abc1234", "scrub_version": 1}
        with mock.patch.object(snap, "mariadb_query", return_value="5"), \
                mock.patch("sys.stdin.isatty", return_value=False), mock.patch("sys.stdout", io.StringIO()):
            with self.assertRaisesRegex(Fail, "not a terminal"):
                snap.confirm(args, [], manifest, "triangle", Path("x.sql.gz"))


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
