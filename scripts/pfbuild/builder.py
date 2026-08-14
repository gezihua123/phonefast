"""Build orchestration — replaces build.sh's build_target + make_archive + build_platforms.

Data-driven by variants.Variant (plain / cgo1 / apple / full). One build_target
per (target, variant); variant controls tags, CGO, and output naming.
"""
from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

from . import log
from . import ffmpeg
from .platform import Target, resolve, FILTER_MAP, BUILD_PLATFORMS_ALL, host_target
from .variants import Variant

# Docs to copy into dist (missing files skipped, no abort).
DIST_DOCS = ["promotional-copy.md", "phonefast-vs-phonemcp.md", "screenshot-mcp-image-content.md"]

# Cache which("zig") / which("upx") — install location doesn't change mid-run.
_ZIG = shutil.which("zig") or None
_UPX = shutil.which("upx") or None


def build_target(
    target: Target,
    variant: Variant,
    ldflags: str,
    dist_dir: Path,
    root_dir: Path,
) -> bool:
    """Build ONE binary for target under the given variant.

    Returns True if built, False if skipped (variant not applicable to target).
    All variant behavior (tags, CGO, naming, platform constraints) comes from
    the Variant table — no if-else per variant here.
    """
    # macos_only variants (cgo1/apple) only make sense on macOS.
    if variant.macos_only and target.goos != "darwin":
        log.warn(f"  {variant.name}: 仅 macOS 有意义，skip {target.goos}/{target.goarch}")
        return False

    # full variant: only embeddable targets (darwin/arm64). Others would be a
    # byte-identical plain (ocr_embed → lib_nolib.go → RuntimeLib=nil).
    if variant.name == "full" and not target.embeddable:
        log.warn(f"  full: {target.goos}/{target.goarch} 不可 embed（无 lib_<goos>_<goarch>.go）— skip")
        return False

    # Output name: platform_prefix controls whether {goos}-{goarch} is included.
    # plain/full include it (--all multi-platform archives need unique names);
    # cgo1/apple don't (macos_only → single platform → no collision).
    if variant.platform_prefix:
        bin_name = f"phonefast-{target.goos}-{target.goarch}{variant.suffix}{target.ext}"
    else:
        bin_name = f"phonefast{variant.suffix}{target.ext}"

    tags = list(variant.tags)

    # full variant: verify the ORT lib to embed exists (warning only, build still runs).
    if variant.name == "full":
        lib = root_dir / "ocr" / "assets" / target.embed_name
        if not (lib.is_file() and lib.stat().st_size > 0):
            log.warn(f"ORT lib missing: {lib}")
            log.warn(f"  Download to ocr/assets/ with: bash ocr/scripts/download.sh {target.goos} {target.goarch}")
            log.warn(f"  Or use: bash ocr/scripts/download.sh --help")

    log.info(f"构建 {target.goos}/{target.goarch}{variant.suffix} ({variant.name}) ...")
    dist_dir.mkdir(parents=True, exist_ok=True)

    # CGO cross-compile env. Variants with force_cgo=True (full, cgo1, apple)
    # must NOT downgrade to CGO=0 on missing FFmpeg — that would lose the
    # apple engine or the embedded ORT lib, the whole point of the variant.
    # force_cgo makes setup_cross_cgo warn-not-downgrade.
    # An explicit CGO_ENABLED=0 from the user/env is still honored (setup_cross_cgo
    # returns early, and env.get falls through to variant.cgo only if unset).
    force_cgo = variant.cgo == "1" and (variant.macos_only or variant.force_cgo)
    env = dict(os.environ)
    env.update(ffmpeg.setup_cross_cgo(target, root_dir, _ZIG, force_cgo=force_cgo))

    # Build go command with tags inline (no fragile index insertion).
    cmd = ["go", "build", "-trimpath"]
    if tags:
        cmd += ["-tags", ",".join(tags)]
    cmd += ["-ldflags", ldflags, "-o", str(dist_dir / bin_name),
            str(root_dir / "cmd" / "phonefast")]

    # CGO: variant.cgo is the default; setup_cross_cgo may override to "0"
    # (plain downgrade on missing FFmpeg). force_cgo variants skip that downgrade.
    env["CGO_ENABLED"] = env.get("CGO_ENABLED", variant.cgo)
    env["GOOS"] = target.goos
    env["GOARCH"] = target.goarch

    ret = subprocess.run(cmd, env=env)
    if ret.returncode != 0:
        log.error(f"go build failed for {target.goos}/{target.goarch}{variant.suffix}")

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
    log.info(f"  {target.goos}/{target.goarch}{variant.suffix} 完成: bin={bin_size}  →  {dist_dir}")
    return True


def make_archive(
    target: Target,
    variant: Variant,
    version: str,
    dist_dir: Path,
) -> None:
    """Package one binary + README + docs into a .tar.gz.

    Archive name mirrors the binary name: plain/full carry {goos}-{goarch},
    cgo1/apple (macos_only) don't — so one darwin archive per variant, no clash.
    """
    if variant.platform_prefix:
        archive_name = f"phonefast-{version}-{target.goos}-{target.goarch}{variant.suffix}"
        bin_name = f"phonefast-{target.goos}-{target.goarch}{variant.suffix}{target.ext}"
    else:
        archive_name = f"phonefast-{version}{variant.suffix}"
        bin_name = f"phonefast{variant.suffix}{target.ext}"
    log.info(f"打包 {variant.name} ...")

    archive_path = dist_dir / f"{archive_name}.tar.gz"
    args = ["tar", "-czf", str(archive_path), bin_name, "README.md", "docs"]
    ret = subprocess.run(args, cwd=str(dist_dir))
    if ret.returncode != 0:
        log.error(f"tar failed for {archive_name}")
    log.info(f"  → {archive_name}.tar.gz  (单文件部署: 解压后直接运行 phonefast-*)")


def build_platforms(
    filter_: str,
    variant: Variant,
    version: str,
    ldflags: str,
    dist_dir: Path,
    root_dir: Path,
) -> None:
    """Build all platforms for the given filter under one variant.

    filter_: "" (native only) | "all" | "macos" | "linux" | "windows"
    variant: the Variant to build (plain/cgo1/apple/full). Single variant per
    invocation — caller runs build.py once per variant for multi-variant builds.
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
        if build_target(target, variant, ldflags, dist_dir, root_dir) and filter_:
            make_archive(target, variant, version, dist_dir)
