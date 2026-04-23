#!/usr/bin/env python3
"""First-time setup for Triangle repositories.

This script is cross-platform (macOS/Linux/Windows) and will:
1. Create a target directory (default: ./triangle)
2. Clone all Triangle repositories into that directory
3. Install project dependencies for each repository
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

REPOS: tuple[tuple[str, str], ...] = (
    ("triangle-cms", "https://github.com/DrexelTriangle/triangle-cms.git"),
    ("wordpress-etl", "https://github.com/DrexelTriangle/wordpress-etl.git"),
    ("Scalene", "https://github.com/DrexelTriangle/Scalene.git"),
)


class SetupError(RuntimeError):
    """Raised when setup cannot continue."""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Clone Triangle repos and install first-time dependencies."
    )
    parser.add_argument(
        "--target-dir",
        default="triangle",
        help="Directory where repositories will be cloned (default: triangle)",
    )
    parser.add_argument(
        "--skip-embeddings",
        action="store_true",
        help="Do not install sentence-transformers for wordpress-etl embeddings.",
    )
    parser.add_argument(
        "--pull",
        action="store_true",
        help="Run git pull in existing repositories after clone/validation.",
    )
    return parser.parse_args()


def command_exists(name: str) -> bool:
    return shutil.which(name) is not None


def run_checked(cmd: list[str], cwd: Path | None = None) -> None:
    where = f" (cwd={cwd})" if cwd else ""
    print(f"\n> {' '.join(cmd)}{where}")
    subprocess.run(cmd, check=True, cwd=str(cwd) if cwd else None)


def ensure_prerequisites(skip_embeddings: bool) -> None:
    missing: list[str] = []

    if not command_exists("git"):
        missing.append("git")
    if not command_exists("npm"):
        missing.append("npm (Node.js 20+ recommended)")
    if not command_exists("go"):
        missing.append("go (1.24+ recommended)")

    if missing:
        raise SetupError(
            "Missing required tools:\n  - " + "\n  - ".join(missing)
        )

    # python/pip come from current interpreter; ensure venv is available.
    try:
        import venv  # noqa: F401
    except Exception as exc:  # pragma: no cover
        raise SetupError("Python venv support is required but unavailable.") from exc

    if not skip_embeddings:
        print("Embedding dependency install is enabled (sentence-transformers).")


def clone_or_update_repos(target_dir: Path, pull: bool) -> None:
    target_dir.mkdir(parents=True, exist_ok=True)

    for name, url in REPOS:
        repo_dir = target_dir / name

        if repo_dir.exists():
            if not (repo_dir / ".git").exists():
                raise SetupError(
                    f"Path exists but is not a git repo: {repo_dir}\n"
                    "Move/delete it or choose a different --target-dir."
                )
            print(f"Repository already exists, skipping clone: {repo_dir}")
        else:
            run_checked(["git", "clone", url, name], cwd=target_dir)

        if pull:
            run_checked(["git", "pull", "--ff-only"], cwd=repo_dir)


def venv_python_path(venv_dir: Path) -> Path:
    if os.name == "nt":
        return venv_dir / "Scripts" / "python.exe"
    return venv_dir / "bin" / "python"


def install_wordpress_etl_deps(target_dir: Path, skip_embeddings: bool) -> None:
    repo_dir = target_dir / "wordpress-etl"
    venv_dir = repo_dir / ".venv"

    if not venv_dir.exists():
        run_checked([sys.executable, "-m", "venv", str(venv_dir)], cwd=repo_dir)

    py = venv_python_path(venv_dir)
    if not py.exists():
        raise SetupError(f"Virtual environment python not found: {py}")

    run_checked([str(py), "-m", "pip", "install", "--upgrade", "pip"], cwd=repo_dir)
    run_checked([str(py), "-m", "pip", "install", "-r", "requirements.txt"], cwd=repo_dir)

    if not skip_embeddings:
        run_checked(
            [str(py), "-m", "pip", "install", "sentence-transformers"],
            cwd=repo_dir,
        )


def install_triangle_cms_deps(target_dir: Path) -> None:
    cms_dir = target_dir / "triangle-cms"
    server_dir = cms_dir / "server"
    frontend_dir = cms_dir / "frontend"

    run_checked(["go", "mod", "download"], cwd=server_dir)

    npm_cmd = ["npm", "ci"] if (frontend_dir / "package-lock.json").exists() else ["npm", "install"]
    run_checked(npm_cmd, cwd=frontend_dir)


def install_scalene_deps(target_dir: Path) -> None:
    repo_dir = target_dir / "Scalene"
    npm_cmd = ["npm", "ci"] if (repo_dir / "package-lock.json").exists() else ["npm", "install"]
    run_checked(npm_cmd, cwd=repo_dir)


def print_summary(target_dir: Path, skip_embeddings: bool) -> None:
    etl_python = venv_python_path(target_dir / "wordpress-etl" / ".venv")
    print("\nSetup complete.")
    print(f"Repositories are ready in: {target_dir}")
    print("\nNext common commands:")
    print(f"- wordpress-etl:  cd {target_dir / 'wordpress-etl'} && {etl_python} main.py")
    if not skip_embeddings:
        print(
            f"- wordpress-etl embeddings:  cd {target_dir / 'wordpress-etl'} && "
            f"{etl_python} main.py --generate-embeddings"
        )
    print(f"- triangle-cms frontend:  cd {target_dir / 'triangle-cms' / 'frontend'} && npm run dev")
    print(f"- Scalene frontend:  cd {target_dir / 'Scalene'} && npm run dev")


def main() -> int:
    args = parse_args()
    target_dir = Path(args.target_dir).expanduser().resolve()

    try:
        ensure_prerequisites(skip_embeddings=args.skip_embeddings)
        clone_or_update_repos(target_dir=target_dir, pull=args.pull)
        install_wordpress_etl_deps(target_dir=target_dir, skip_embeddings=args.skip_embeddings)
        install_triangle_cms_deps(target_dir=target_dir)
        install_scalene_deps(target_dir=target_dir)
        print_summary(target_dir=target_dir, skip_embeddings=args.skip_embeddings)
        return 0
    except SetupError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    except subprocess.CalledProcessError as exc:
        print(f"ERROR: command failed with exit code {exc.returncode}: {exc.cmd}", file=sys.stderr)
        return exc.returncode


if __name__ == "__main__":
    raise SystemExit(main())
