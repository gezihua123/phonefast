#!/usr/bin/env python3
"""phonefast build tool — builds phonefast binaries (plain + optional -full).

Usage:
  python3 scripts/build.py                    # native darwin-arm64 plain
  python3 scripts/build.py --all              # all platforms
  python3 scripts/build.py --full             # also build -full (embed ORT lib)
  python3 scripts/build.py --macos --full-only
  python3 scripts/build.py --version 1.0.0 --clean
  python3 scripts/build.py --all --ensure-ffmpeg  # compile FFmpeg libs if missing

Standard library only.
"""
from __future__ import annotations

import argparse
import datetime
import os
import shutil
import subprocess
import sys
from pathlib import Path

SCRIPTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS_DIR))

from pfbuild import log, assets, builder, ffmpeg
from pfbuild.platform import host_target, TARGETS


def _project_root() -> Path:
    return SCRIPTS_DIR.parent


def _version(default: str = "dev") -> str:
    if os.environ.get("VERSION"):
        return os.environ["VERSION"]
    try:
        out = subprocess.run(
            ["git", "describe", "--tags", "--abbrev=0"],
            capture_output=True, text=True, cwd=str(_project_root()),
        ).stdout.strip()
        return out.lstrip("v") if out else default
    except Exception:
        return default


def _git_commit() -> str:
    try:
        out = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            capture_output=True, text=True, cwd=str(_project_root()),
        ).stdout.strip()
        return out or "unknown"
    except Exception:
        return "unknown"


def _go_version() -> str:
    try:
        return subprocess.run(["go", "version"], capture_output=True, text=True).stdout.strip()
    except Exception:
        return "unknown"


def main() -> None:
    parser = argparse.ArgumentParser(prog="build.py", description="Build phonefast binaries")
    g = parser.add_mutually_exclusive_group()
    for flag in ("--all", "--macos", "--linux", "--windows"):
        g.add_argument(flag, action="store_const", const=flag.lstrip("-"), dest="filter")
    g2 = parser.add_mutually_exclusive_group()
    g2.add_argument("--full", action="store_true", help="Also build -full self-contained (embed ORT lib)")
    g2.add_argument("--full-only", action="store_true", help="Only build -full (skip plain)")
    parser.add_argument("--version", default=None, help="Version string (default: git tag or 'dev')")
    parser.add_argument("--clean", action="store_true", help="Clean dist/ before building")
    parser.add_argument("--ensure-ffmpeg", action="store_true", help="Compile FFmpeg static libs if missing")
    parser.set_defaults(filter=None)
    args = parser.parse_args()

    root = _project_root()
    assets_dir = root / "assets"
    dist_dir = root / "dist" / "dev"

    # 1. Sync assets (jar + OCR models).
    assets.sync_all(assets_dir, root)

    # 2. Version + LDFLAGS.
    version = args.version or _version()
    build_time = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    git_commit = _git_commit()
    ldflags = f"-s -w -X main.Version={version} -X main.BuildTime={build_time} -X main.GitCommit={git_commit}"

    # 3. Clean?
    if args.clean:
        log.info(f"清理构建目录: {dist_dir}")
        shutil.rmtree(dist_dir, ignore_errors=True)

    # 4. Ensure FFmpeg (if requested).
    filter_ = args.filter or ""
    if args.ensure_ffmpeg:
        log.info("[1/2] 确保静态 FFmpeg 库就绪...")
        if filter_ == "all":
            ffmpeg_targets = [t.ffmpeg_target for t in TARGETS.values() if t.default_release]
        elif filter_:
            ffmpeg_targets = [t.ffmpeg_target for k, t in TARGETS.items() if t.goos == filter_ and t.default_release]
        else:
            ffmpeg_targets = [host_target().ffmpeg_target]
        ffmpeg.ensure_ffmpeg_compiled(ffmpeg_targets, root)
        log.info("")

    # 5. Build.
    build_full = ""
    if args.full_only:
        build_full = "full-only"
    elif args.full:
        build_full = "full"

    target_desc = filter_ or f"当前平台 ({host_target().goos}/{host_target().goarch})"
    log.info(f"phonefast {version}  构建开始")
    log.info(f"目标: {target_desc}  变体: {build_full or 'plain'}")
    print()

    builder.build_platforms(filter_, build_full, version, ldflags, dist_dir, root)

    # 6. Summary.
    print()
    print("═" * 80)
    print(f"  phonefast {version}  构建完成")
    print("═" * 80)
    print(f"  Go 版本:    {_go_version()}")
    print(f"  Git commit: {git_commit}")
    print(f"  构建时间:   {build_time}")
    print(f"  产物目录:   {dist_dir}/")
    print()
    for p in sorted(dist_dir.rglob("*")):
        if p.is_file():
            sz = log.human_size(p)
            print(f"  {sz:>8}  {p.relative_to(dist_dir)}")
    print()
    print("  部署: 单文件 — 直接运行 phonefast-* 即可（jar 已内嵌）")
    print("═" * 80)


if __name__ == "__main__":
    main()
