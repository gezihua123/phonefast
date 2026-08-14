"""Asset preparation — scrcpy-server.jar sync.

OCR model and ONNX Runtime downloads live in the OCR module:
    bash ocr/scripts/download.sh
"""
from __future__ import annotations

import shutil
from pathlib import Path

from . import log


def sync_jar(assets_dir: Path, root_dir: Path) -> None:
    """Ensure assets/scrcpy-server.jar exists (copy from android/ or error)."""
    jar = assets_dir / "scrcpy-server.jar"
    ver = assets_dir / "scrcpy-server.version"
    if jar.is_file():
        log.info("assets/scrcpy-server.jar already present")
        return
    src_jar = root_dir / "android" / "scrcpy-server.jar"
    src_ver = root_dir / "android" / "scrcpy-server.version"
    if src_jar.is_file():
        log.info("sync jar -> assets/")
        shutil.copy2(src_jar, jar)
        if src_ver.is_file():
            shutil.copy2(src_ver, ver)
    else:
        log.error(
            "assets/scrcpy-server.jar and android/scrcpy-server.jar both missing.\n"
            "Run: bash scripts/build-server.sh"
        )


def sync_all(assets_dir: Path, root_dir: Path) -> None:
    """Ensure scrcpy jar is ready for embed."""
    assets_dir.mkdir(parents=True, exist_ok=True)
    sync_jar(assets_dir, root_dir)
