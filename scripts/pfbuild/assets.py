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


def ensure_ocr_model_placeholders(root_dir: Path) -> None:
    """Create empty placeholder model files so //go:embed compiles without a
    download.

    The PP-OCR model files are gitignored download artifacts
    (ocr/scripts/download.sh). //go:embed fails the build on a missing file,
    so absent/empty models get an empty placeholder: embed yields nil bytes,
    and the engine loads models from disk at runtime (bridge variants) or
    errors with a download hint. Only the -full variant needs real bytes —
    builder.py warns when it builds with empty models.
    """
    for name in ("ppocr-det.onnx", "ppocr-rec.onnx"):
        p = root_dir / "ocr" / "assets" / name
        if not p.is_file() or p.stat().st_size == 0:
            p.parent.mkdir(parents=True, exist_ok=True)
            if not p.is_file():
                p.touch()
            log.warn(
                f"ocr/assets/{name} missing/empty — placeholder created (models not embedded).\n"
                "  Run: bash ocr/scripts/download.sh models  (needed for -full self-contained builds;\n"
                "  plain/cgo1/apple load models from disk at runtime)"
            )


def sync_all(assets_dir: Path, root_dir: Path) -> None:
    """Ensure scrcpy jar is ready for embed + OCR model placeholders exist."""
    assets_dir.mkdir(parents=True, exist_ok=True)
    sync_jar(assets_dir, root_dir)
    ensure_ocr_model_placeholders(root_dir)
