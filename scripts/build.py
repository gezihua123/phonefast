#!/usr/bin/env python3
"""phonefast build tool — builds phonefast binaries for one OCR variant.

Usage:
  python3 scripts/build.py                          # native plain
  python3 scripts/build.py --variant cgo1           # native cgo1 (apple engine)
  python3 scripts/build.py --variant apple          # native apple-only
  python3 scripts/build.py --variant full           # native -full (embed ORT lib)
  python3 scripts/build.py --all                    # all platforms, plain
  python3 scripts/build.py --all --variant full      # all platforms, -full
  python3 scripts/build.py --version 1.0.0 --clean
  python3 scripts/build.py --all --ensure-ffmpeg     # compile FFmpeg libs if missing

Legacy aliases (mutually exclusive with --variant):
  --full        = --variant full
  --full-only   = --variant full

One variant per invocation. To build multiple variants, run build.py once each.

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
from pfbuild.variants import Variant, get as get_variant


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
        # filter value must match Target.goos for FILTER_MAP / ensure_ffmpeg lookups.
        # --macos selects darwin targets (goos="darwin"); the others already match.
        val = "darwin" if flag == "--macos" else flag.lstrip("-")
        g.add_argument(flag, action="store_const", const=val, dest="filter")
    # Variant selection: --variant (new) is mutually exclusive with --full/--full-only (legacy).
    gv = parser.add_mutually_exclusive_group()
    gv.add_argument("--variant", choices=["plain", "cgo1", "apple", "full"], default="plain",
                    help="OCR build variant (default: plain). cgo1/apple = macOS only.")
    gv.add_argument("--full", action="store_true", help="Legacy: = --variant full (embed ORT lib)")
    gv.add_argument("--full-only", action="store_true", help="Legacy: = --variant full")
    parser.add_argument("--version", default=None, help="Version string (default: git tag or 'dev')")
    parser.add_argument("--clean", action="store_true", help="Clean dist/ before building")
    parser.add_argument("--ensure-ffmpeg", action="store_true", help="Compile FFmpeg static libs if missing")
    parser.set_defaults(filter=None)
    args = parser.parse_args()

    # Resolve variant: --full/--full-only both map to "full"; else use --variant.
    variant_name = "full" if (args.full or args.full_only) else args.variant
    variant = get_variant(variant_name)

    root = _project_root()
    assets_dir = root / "assets"
    dist_dir = root / "dist" / "dev"

    # 1. Sync assets (jar).
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

    # 5. Build one variant across the selected platforms.
    target_desc = filter_ or f"当前平台 ({host_target().goos}/{host_target().goarch})"
    log.info(f"phonefast {version}  构建开始")
    log.info(f"目标: {target_desc}  变体: {variant.name}")
    print()

    builder.build_platforms(filter_, variant, version, ldflags, dist_dir, root)

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
