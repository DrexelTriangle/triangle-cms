#!/usr/bin/env python3
"""First-time setup for Triangle repositories.

This script is cross-platform (macOS/Linux/Windows) and will:
1. Determine a target directory
   (default: parent of current triangle-cms checkout, else ./triangle)
2. Clone all Triangle repositories into that directory
3. Install project dependencies for each repository
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPOS: tuple[tuple[str, str], ...] = (
    ("triangle-cms", "https://github.com/DrexelTriangle/triangle-cms.git"),
    ("wordpress-etl", "https://github.com/DrexelTriangle/wordpress-etl.git"),
    ("Scalene", "https://github.com/DrexelTriangle/Scalene.git"),
)
PRIMARY_REPO_NAME = "triangle-cms"


class SetupError(RuntimeError):
    """Raised when setup cannot continue."""


def infer_default_target_dir() -> str:
    script_root = Path(__file__).resolve().parent.parent
    if script_root.name == PRIMARY_REPO_NAME and (script_root / ".git").exists():
        # If run from an existing triangle-cms checkout, place sibling repos next to it.
        return str(script_root.parent)
    return "triangle"


def parse_args() -> argparse.Namespace:
    default_target_dir = infer_default_target_dir()
    parser = argparse.ArgumentParser(
        description="Clone Triangle repos and install first-time dependencies."
    )
    parser.add_argument(
        "--target-dir",
        default=default_target_dir,
        help=f"Directory where repositories will be cloned (default: {default_target_dir})",
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


def resolve_command(cmd: list[str]) -> list[str]:
    """Resolve executable path for robust cross-platform subprocess execution."""
    if not cmd:
        return cmd

    exe = cmd[0]
    resolved = shutil.which(exe)

    if os.name == "nt" and resolved is None and Path(exe).suffix == "":
        # Windows tools are often command shims (e.g., npm.cmd).
        for suffix in (".cmd", ".bat", ".exe"):
            resolved = shutil.which(exe + suffix)
            if resolved:
                break

    if resolved:
        return [resolved, *cmd[1:]]
    return cmd


def run_checked(cmd: list[str], cwd: Path | None = None) -> None:
    where = f" (cwd={cwd})" if cwd else ""
    print(f"\n> {' '.join(cmd)}{where}")
    if cwd is not None and not cwd.exists():
        raise SetupError(f"Working directory does not exist: {cwd}")

    resolved_cmd = resolve_command(cmd)
    try:
        subprocess.run(resolved_cmd, check=True, cwd=str(cwd) if cwd else None)
    except FileNotFoundError as exc:
        raise SetupError(
            "Failed to start command. Ensure the tool is installed and on PATH.\n"
            f"Command: {' '.join(cmd)}\n"
            f"Resolved: {' '.join(resolved_cmd)}\n"
            f"cwd: {cwd if cwd else Path.cwd()}"
        ) from exc


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


def normalize_wordpress_requirements(requirements_path: Path, skip_embeddings: bool) -> Path:
    """Return a temporary requirements file with local compatibility fixes."""
    normalized_lines: list[str] = []

    for raw_line in requirements_path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        lowered = line.lower()

        if skip_embeddings and lowered.startswith("sentence-transformers"):
            continue

        # Guard against known bad upstream pin ("numba>=2.2.6").
        if lowered.startswith("numba>=2"):
            normalized_lines.append("numba")
            continue

        normalized_lines.append(raw_line)

    tmp = tempfile.NamedTemporaryFile(
        mode="w",
        encoding="utf-8",
        prefix="wordpress-etl-req-",
        suffix=".txt",
        delete=False,
    )
    try:
        tmp.write("\n".join(normalized_lines).rstrip() + "\n")
    finally:
        tmp.close()

    return Path(tmp.name)


def install_wordpress_etl_deps(target_dir: Path, skip_embeddings: bool) -> None:
    repo_dir = target_dir / "wordpress-etl"
    venv_dir = repo_dir / ".venv"

    if not venv_dir.exists():
        run_checked([sys.executable, "-m", "venv", str(venv_dir)], cwd=repo_dir)

    py = venv_python_path(venv_dir)
    if not py.exists():
        raise SetupError(f"Virtual environment python not found: {py}")

    run_checked([str(py), "-m", "pip", "install", "--upgrade", "pip"], cwd=repo_dir)
    tmp_requirements = normalize_wordpress_requirements(
        requirements_path=repo_dir / "requirements.txt",
        skip_embeddings=skip_embeddings,
    )
    try:
        run_checked(
            [str(py), "-m", "pip", "install", "-r", str(tmp_requirements)],
            cwd=repo_dir,
        )
    finally:
        try:
            tmp_requirements.unlink(missing_ok=True)
        except OSError:
            pass


def install_triangle_cms_deps(target_dir: Path) -> None:
    cms_dir = target_dir / "triangle-cms"
    server_dir = cms_dir / "server"
    frontend_dir = cms_dir / "frontend"

    run_checked(["go", "mod", "download"], cwd=server_dir)
    install_node_deps(frontend_dir)


def install_scalene_deps(target_dir: Path) -> None:
    repo_dir = target_dir / "Scalene"
    install_node_deps(repo_dir)


def install_node_deps(repo_dir: Path) -> None:
    """Install Node dependencies for local development setup."""
    run_checked(["npm", "install"], cwd=repo_dir)


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
