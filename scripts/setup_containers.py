#!/usr/bin/env python3
"""Cross-platform Docker Compose setup utility for Triangle CMS."""

from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Start Triangle CMS Docker services, optionally resetting volumes."
    )
    parser.add_argument(
        "--reset-data",
        action="store_true",
        help="Run `compose down -v --remove-orphans` before starting containers.",
    )
    return parser.parse_args()


def command_exists(name: str) -> bool:
    return shutil.which(name) is not None


def can_run(cmd: list[str]) -> bool:
    try:
        subprocess.run(
            cmd,
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        return True
    except (FileNotFoundError, subprocess.CalledProcessError):
        return False


def detect_compose_cmd() -> list[str]:
    if not command_exists("docker"):
        print("docker is not installed or not on PATH", file=sys.stderr)
        raise SystemExit(1)

    if can_run(["docker", "compose", "version"]):
        return ["docker", "compose"]

    if command_exists("docker-compose"):
        return ["docker-compose"]

    print("docker compose plugin (or docker-compose) is required", file=sys.stderr)
    raise SystemExit(1)


def run_checked(cmd: list[str], cwd: Path) -> None:
    subprocess.run(cmd, check=True, cwd=str(cwd))


def main() -> int:
    args = parse_args()
    root_dir = Path(__file__).resolve().parent.parent
    compose_cmd = detect_compose_cmd()

    try:
        if args.reset_data:
            print("Resetting compose services and volumes...")
            run_checked(compose_cmd + ["down", "-v", "--remove-orphans"], cwd=root_dir)

        print("Starting mariadb, cms, loki, and promtail...")
        run_checked(compose_cmd + ["up", "-d", "--build", "--remove-orphans"], cwd=root_dir)
    except subprocess.CalledProcessError as exc:
        return exc.returncode

    compose_text = " ".join(compose_cmd)
    print()
    print("Stack is up. Useful commands:")
    print(f"  {compose_text} ps")
    print(f"  {compose_text} logs -f cms")
    print(f"  {compose_text} logs -f promtail")
    print(f"  {compose_text} down")
    print(f"  {compose_text} down -v   # remove volumes (DB/Loki data)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
