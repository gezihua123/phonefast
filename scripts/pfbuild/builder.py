"""Build orchestration — replaces build.sh's build_target + make_archive + build_platforms.

Produces plain (no embed) and -full (ocr_embed tag) binaries.
"""
from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

from . import log
from . import assets
from . import ffmpeg
from .platform import Target, resolve, FILTER_MAP, BUILD_PLATFORMS_ALL, host_target

# Docs to copy into dist (missing files skipped, no abort).
DIST_DOCS = ["promotional-copy.md", "phonefast-vs-phonemcp.md", "screenshot-mcp-image-content.md"]

# Cache which("zig") / which("upx") — install location doesn't change mid-run.
_ZIG = shutil.which("zig") or None
_UPX = shutil.which("upx") or None


def build_target(
    target: Target,
    variant: str,
    ldflags: str,
    dist_dir: Path,
    root_dir: Path,
) -> bool:
    """Build ONE binary for target. Returns True if built, False if skipped.

    variant: "" (plain, loads system lib) | "-full" (embed ORT lib via ocr_embed tag).
    """
    bin_name = f"phonefast-{target.goos}-{target.goarch}{variant}{target.ext}"
    tags = []

    # -full variant: embed ORT lib + ensure it's staged.
    if variant == "-full":
        if not target.embeddable:
            log.warn(f"  -full: {target.goos}/{target.goarch} has no embed lib (lib_nolib.go) — -full = plain, skipping")
            return False
        tags.append("ocr_embed")
        lib = root_dir / "assets" / "ocr" / target.embed_name
        if not (lib.is_file() and lib.stat().st_size > 0):
            log.info(f"ORT 运行时库缺失, 下载中 (target={target.goos}/{target.goarch})...")
            assets.sync_lib(target)

    log.info(f"构建 {target.goos}/{target.goarch}{variant} ...")
    dist_dir.mkdir(parents=True, exist_ok=True)

    # CGO cross-compile env.
    env = dict(os.environ)
    env.update(ffmpeg.setup_cross_cgo(target, root_dir, _ZIG))

    # Build go command with tags inline (no fragile index insertion).
    cmd = ["go", "build", "-trimpath"]
    if tags:
        cmd += ["-tags", ",".join(tags)]
    cmd += ["-ldflags", ldflags, "-o", str(dist_dir / bin_name),
            str(root_dir / "cmd" / "phonefast")]

    env["CGO_ENABLED"] = env.get("CGO_ENABLED", "1")
    env["GOOS"] = target.goos
    env["GOARCH"] = target.goarch

    ret = subprocess.run(cmd, env=env)
    if ret.returncode != 0:
        log.error(f"go build failed for {target.goos}/{target.goarch}{variant}")

    # Copy docs.
    docs_dir = dist_dir / "docs"
    docs_dir.mkdir(exist_ok=True)
    shutil.copy2(root_dir / "README.md", dist_dir / "README.md")
    for doc in DIST_DOCS:
        src = root_dir / "docs" / doc
        if src.is_file():
            shutil.copy2(src, docs_dir / doc)

    # UPX (optional, failure tolerated).
    if _UPX:
        subprocess.run([_UPX, "-q", str(dist_dir / bin_name)],
                       stderr=subprocess.DEVNULL, stdout=subprocess.DEVNULL)

    bin_size = log.human_size(dist_dir / bin_name)
    log.info(f"  {target.goos}/{target.goarch}{variant} 完成: bin={bin_size}  →  {dist_dir}")
    return True


def make_archive(
    target: Target,
    variant: str,
    version: str,
    dist_dir: Path,
) -> None:
    """Package one binary + README + docs into a .tar.gz."""
    archive_name = f"phonefast-{version}-{target.goos}-{target.goarch}{variant}"
    bin_name = f"phonefast-{target.goos}-{target.goarch}{variant}{target.ext}"
    log.info(f"打包 {target.goos}/{target.goarch}{variant} ...")

    archive_path = dist_dir / f"{archive_name}.tar.gz"
    args = ["tar", "-czf", str(archive_path), bin_name, "README.md", "docs"]
    ret = subprocess.run(args, cwd=str(dist_dir))
    if ret.returncode != 0:
        log.error(f"tar failed for {archive_name}")
    log.info(f"  → {archive_name}.tar.gz  (单文件部署: 解压后直接运行 phonefast-*)")


def build_platforms(
    filter_: str,
    build_full: str,
    version: str,
    ldflags: str,
    dist_dir: Path,
    root_dir: Path,
) -> None:
    """Build all platforms for the given filter.

    filter_: "" (native only) | "all" | "macos" | "linux" | "windows"
    build_full: "" (plain only) | "full" (plain + -full) | "full-only" (-full only)
    """
    if filter_ == "all":
        platforms = BUILD_PLATFORMS_ALL
    elif filter_:
        platforms = FILTER_MAP.get(filter_, [])
    else:
        # Native only.
        host = host_target()
        platforms = [f"{host.goos}/{host.goarch}"]

    for plat in platforms:
        target = resolve(plat)

        # Plain build (unless full-only).
        if build_full != "full-only":
            if build_target(target, "", ldflags, dist_dir, root_dir) and filter_:
                make_archive(target, "", version, dist_dir)

        # -full build (if requested, only for embeddable targets).
        if build_full:
            if build_target(target, "-full", ldflags, dist_dir, root_dir) and filter_:
                make_archive(target, "-full", version, dist_dir)
