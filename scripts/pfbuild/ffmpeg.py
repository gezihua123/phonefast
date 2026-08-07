"""FFmpeg/zig/CGO cross-compile environment setup — replaces build.sh's setup_cross_cgo.

Returns an env dict (CC/CXX/PKG_CONFIG_PATH/CGO_ENABLED) to merge into the
go build subprocess env. download-ffmpeg.sh stays shell (Python calls it).
"""
from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

from . import log
from .platform import Target, host_target


def setup_cross_cgo(
    target: Target,
    root_dir: Path,
    zig_path: str | None = None,
    force_cgo: bool = False,
) -> dict[str, str]:
    """Determine CGO env for cross-compiling to target.

    Returns a dict of env vars to set/override for `go build`.

    force_cgo: when True, do NOT downgrade CGO_ENABLED to 0 on missing
    FFmpeg — warn instead and let the build proceed (relying on system FFmpeg
    via pkg-config). Used by cgo1/apple variants where downgrading would
    silently drop the apple engine (the whole point of the variant). Plain
    keeps the legacy downgrade-for-back-compat behavior.
    """
    env: dict[str, str] = {}
    if os.environ.get("CGO_ENABLED") == "0":
        return env  # already disabled, nothing to set up

    host = host_target()
    is_native = target.goos == host.goos and target.goarch == host.goarch

    # ── Toolchain (CC/CXX) ──
    if is_native:
        pass  # default compiler
    elif target.goos == "darwin":
        # zig cc can't find macOS SDK system libs; use clang directly.
        env["CC"] = "clang"
        env["CXX"] = "clang++"
    else:
        # zig cc for linux/windows cross.
        if not zig_path:
            log.warn("zig 未安装, 降级 CGO_ENABLED=0")
            log.warn(f"  手动备选: bash scripts/cross-build-ffmpeg.sh {target.ffmpeg_target}")
            env["CGO_ENABLED"] = "0"
            return env
        env["CC"] = f"zig cc -target {target.zig_target}"
        env["CXX"] = f"zig c++ -target {target.zig_target}"

    # ── FFmpeg static libs ──
    ffmpeg_dir = root_dir / "build" / "cross-ffmpeg" / target.ffmpeg_target
    pkgconfig = ffmpeg_dir / "lib" / "pkgconfig"

    if pkgconfig.is_dir():
        env["PKG_CONFIG_PATH"] = str(pkgconfig)
        native_tag = " (native)" if is_native else ""
        log.info(f"  CGO: CC={env.get('CC', 'default')}, FFmpeg={target.ffmpeg_target}{native_tag}")
        return env

    # FFmpeg missing — try auto-download (interactive terminal only).
    log.warn(f"FFmpeg 静态库不存在: {ffmpeg_dir}")
    if sys.stdout.isatty() and _try_download_ffmpeg(root_dir, target.ffmpeg_target, pkgconfig):
        env["PKG_CONFIG_PATH"] = str(pkgconfig)
        return env

    # FFmpeg unavailable and download didn't help.
    if force_cgo:
        # cgo1/apple variant: keep CGO=1, fall back to system FFmpeg via pkg-config.
        # Downgrading would lose the apple engine — the reason this variant exists.
        log.warn(f"  手动备选: bash scripts/cross-build-ffmpeg.sh {target.ffmpeg_target}")
        log.warn("  force_cgo=True: 保持 CGO_ENABLED=1（依赖系统 FFmpeg pkg-config）")
        return env

    # Plain variant: legacy downgrade to CGO=0 (loses astiav + apple engine).
    log.warn(f"  手动备选: bash scripts/cross-build-ffmpeg.sh {target.ffmpeg_target}")
    log.warn("  或跳过 CGO: CGO_ENABLED=0 go build ./cmd/phonefast/")
    log.warn("  降级 CGO_ENABLED=0")
    env["CGO_ENABLED"] = "0"
    return env


def _try_download_ffmpeg(root_dir: Path, ffmpeg_target: str, pkgconfig: Path) -> bool:
    """Attempt to download+build FFmpeg via download-ffmpeg.sh. Returns True on success."""
    dl_script = root_dir / "scripts" / "download-ffmpeg.sh"
    if not dl_script.is_file():
        return False
    log.info(f"  尝试下载 FFmpeg: bash {dl_script} {ffmpeg_target}")
    ret = subprocess.run(["bash", str(dl_script), ffmpeg_target], stderr=subprocess.DEVNULL)
    return ret.returncode == 0 and pkgconfig.is_dir()


def ensure_ffmpeg_compiled(targets: list[str], root_dir: Path) -> None:
    """Ensure FFmpeg static libs exist for all targets, compiling if missing.

    Replaces build_local.sh's FFmpeg-ensure loop: for each target, check
    build/cross-ffmpeg/<target>/lib/libavcodec.a; if missing, call
    cross-build-ffmpeg.sh. Aborts on compile failure.
    """
    for ffmpeg_target in targets:
        libavcodec = root_dir / "build" / "cross-ffmpeg" / ffmpeg_target / "lib" / "libavcodec.a"
        if libavcodec.is_file():
            log.info(f"  ✓ {ffmpeg_target} (已存在, 跳过)")
            continue
        log.info(f"  编译 FFmpeg: {ffmpeg_target}")
        ret = subprocess.run(
            ["bash", str(root_dir / "scripts" / "cross-build-ffmpeg.sh"), ffmpeg_target],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        if ret.returncode != 0:
            log.error(f"  ✗ FFmpeg 编译失败: {ffmpeg_target}\n    手动排查: bash scripts/cross-build-ffmpeg.sh {ffmpeg_target}")
        log.info(f"  ✓ {ffmpeg_target}")

