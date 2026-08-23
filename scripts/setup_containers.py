#!/usr/bin/env python3
"""Cross-platform helper to start local Docker Compose services."""

from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
from pathlib import Path


def check_command(cmd: list[str]) -> bool:
    try:
        subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        return True
    except (subprocess.CalledProcessError, FileNotFoundError):
        return False


def run_command(cmd: list[str], *, cwd: Path) -> None:
    subprocess.run(cmd, check=True, cwd=str(cwd))


def resolve_compose_command() -> list[str]:
    if check_command(["docker", "compose", "version"]):
        return ["docker", "compose"]

    if shutil.which("docker-compose"):
        return ["docker-compose"]

    print("docker compose plugin (or docker-compose) is required", file=sys.stderr)
    sys.exit(1)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Start local compose services")
    parser.add_argument(
        "--reset-data",
        action="store_true",
        help="stop stack and remove compose volumes before starting",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    root_dir = Path(__file__).resolve().parent.parent

    if shutil.which("docker") is None:
        print("docker is not installed or not on PATH", file=sys.stderr)
        return 1

    compose_cmd = resolve_compose_command()

    if args.reset_data:
        print("Resetting compose services and volumes...")
        run_command([*compose_cmd, "down", "-v", "--remove-orphans"], cwd=root_dir)

    print("Starting mariadb, cms, and prometheus...")
    run_command([*compose_cmd, "up", "-d", "--build", "--remove-orphans"], cwd=root_dir)

    printable = " ".join(compose_cmd)
    print("\nStack is up. Useful commands:")
    print(f"  {printable} ps")
    print(f"  {printable} logs -f cms")
    print(f"  {printable} down")
    print(f"  {printable} down -v   # remove volumes (DB/Prometheus data)")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
