"""Asset preparation — OCR models + ORT runtime lib + scrcpy jar.

Merges build.sh's sync_assets/sync_ocr_lib and download-ocr-models.sh's
download_models/download_lib into one module. build.py calls these directly
(no shell-out to download-ocr-models.sh).
"""
from __future__ import annotations

import glob
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile
from pathlib import Path

from . import log
from .platform import Target, host_target

# ── Constants ────────────────────────────────────────────────────

ORT_VERSION = os.environ.get("ORT_VERSION", "1.27.1")

HF_BASE = "https://huggingface.co/SWHL/RapidOCR/resolve/main/PP-OCRv3"
DET_URL = f"{HF_BASE}/ch_PP-OCRv3_det_infer.onnx"
REC_URL = f"{HF_BASE}/ch_PP-OCRv3_rec_infer.onnx"

MODELS = ["ppocr-det.onnx", "ppocr-rec.onnx"]


# ── HTTP helper ──────────────────────────────────────────────────

def http_get(url: str, dest: Path) -> bool:
    """Download url → dest using curl or wget (with retry + timeout). Returns True on success."""
    if shutil.which("curl"):
        ret = subprocess.run(["curl", "-fsSL", "--retry", "3", "--connect-timeout", "10", url, "-o", str(dest)])
        return ret.returncode == 0
    if shutil.which("wget"):
        ret = subprocess.run(["wget", "-q", "--retry-connrefused", "--timeout=10", url, "-O", str(dest)])
        return ret.returncode == 0
    log.warn(f"Neither curl nor wget found; cannot download {url}")
    return False


# ── Models (PP-OCR v3 ONNX, platform-independent) ────────────────

def sync_models(assets_dir: Path) -> None:
    """Ensure ppocr-det.onnx + ppocr-rec.onnx exist (non-empty) in assets_dir.

    Source 1: RapidOCR HuggingFace (authoritative, version-pinned).
    Source 2: local pip rapidocr_onnxruntime (offline fallback).
    Aborts on final failure (both missing).
    """
    det = assets_dir / "ppocr-det.onnx"
    rec = assets_dir / "ppocr-rec.onnx"

    if _nonempty(det) and _nonempty(rec):
        log.info("assets/ocr/ppocr-det.onnx + ppocr-rec.onnx 已就绪")
        return

    # Source 1: HuggingFace.
    if not _nonempty(det):
        log.info("Downloading models from HuggingFace (RapidOCR PP-OCRv3)...")
        if http_get(DET_URL, det) and http_get(REC_URL, rec):
            log.info(f"det: {log.human_size(det)}")
            log.info(f"rec: {log.human_size(rec)}")
        else:
            log.warn("HuggingFace download failed; trying local pip rapidocr_onnxruntime...")
            det.unlink(missing_ok=True)
            rec.unlink(missing_ok=True)

    # Source 2: pip rapidocr_onnxruntime.
    if not _nonempty(det):
        try:
            site = subprocess.run(
                ["python3", "-c", "import site; print(site.getsitepackages()[0])"],
                capture_output=True, text=True,
            ).stdout.strip()
        except Exception:
            site = ""
        if site and Path(site, "rapidocr_onnxruntime/models").is_dir():
            src = Path(site, "rapidocr_onnxruntime/models")
            det_src = src / "ch_PP-OCRv3_det_infer.onnx"
            rec_src = src / "ch_PP-OCRv3_rec_infer.onnx"
            if det_src.is_file() and rec_src.is_file():
                log.info(f"Found models from pip rapidocr_onnxruntime: {src}")
                shutil.copy2(det_src, det)
                shutil.copy2(rec_src, rec)
                log.info(f"det: {log.human_size(det)}")
                log.info(f"rec: {log.human_size(rec)}")
            else:
                log.warn("rapidocr_onnxruntime found but v3 models missing.")
        else:
            log.warn("rapidocr_onnxruntime not installed via pip.")

    # Final sanity.
    for f in MODELS:
        if not _nonempty(assets_dir / f):
            log.warn(f"Model {f} is missing or empty. OCR will return ErrNotAvailable.")
            log.warn("Install rapidocr_onnxruntime (pip) or ensure network to huggingface.co.")
            sys.exit(1)


# ── ORT runtime lib (platform-specific) ──────────────────────────

def sync_lib(target: Target, force: bool = False) -> None:
    """Ensure the platform ORT lib is staged in assets_dir for embedding.

    Source 1: system install (host target only — brew/apt).
    Source 2: ONNX Runtime GitHub release v{ORT_VERSION} (any target, cross-friendly).
    Skips if already present (unless force=True).
    """
    assets_dir = _assets_dir()
    embed_path = assets_dir / target.embed_name

    # Skip if present (unless --force).
    if not force and _nonempty(embed_path):
        log.info(f"Already present: {target.embed_name} ({log.human_size(embed_path)}) — use --force to re-fetch")
        return

    # Source 1: system install (host target only).
    if target == host_target():
        for sys_path in _system_lib_paths(target):
            for expanded in glob.glob(sys_path):
                if Path(expanded).is_file():
                    log.info(f"Found ONNX Runtime (system): {expanded}")
                    shutil.copy2(expanded, embed_path)
                    log.info(f"Copied: {target.embed_name} ({log.human_size(embed_path)})")
                    return

    # Source 2: GitHub release — download + stream-extract only the lib file.
    pkg = f"onnxruntime-{target.ort_suffix}-{ORT_VERSION}"
    ext = ".zip" if target.ort_suffix.startswith("win-") else ".tgz"
    url = f"https://github.com/microsoft/onnxruntime/releases/download/v{ORT_VERSION}/{pkg}{ext}"
    log.info(f"Downloading ONNX Runtime from GitHub release: {pkg}...")
    with tempfile.TemporaryDirectory() as tmpdir:
        tmp = Path(tmpdir)
        if not http_get(url, tmp / "pkg"):
            log.error(f"Failed to download {url}")
        # Stream-extract only the needed lib file (not the whole archive).
        lib_bytes = _extract_one(tmp / "pkg", target.lib_name, ext)
        if lib_bytes is None:
            log.error(f"Library {target.lib_name} not found in archive.")
        embed_path.write_bytes(lib_bytes)
        log.info(f"Extracted: {target.embed_name} ({log.human_size(embed_path)})")


def _extract_one(archive: Path, name: str, ext: str) -> bytes | None:
    """Extract only the file named `name` from the archive, resolving symlinks.

    Returns the file's bytes, or None if not found. Avoids extracting the
    full archive to disk (headers, test binaries, etc.) — just the one lib.
    """
    if ext == ".zip":
        with zipfile.ZipFile(archive) as z:
            for info in z.infolist():
                if Path(info.filename).name == name:
                    return z.read(info.filename)
    else:
        with tarfile.open(archive, "r:gz") as t:
            for member in t.getmembers():
                if Path(member.name).name == name:
                    f = t.extractfile(member)
                    if f is None:
                        continue
                    data = f.read()
                    # If it's a symlink, follow it within the archive.
                    if member.issym() or member.islnk():
                        target_name = member.linkname
                        for m2 in t.getmembers():
                            if m2.name.endswith(target_name) and m2.isfile():
                                f2 = t.extractfile(m2)
                                if f2:
                                    return f2.read()
                    return data
    return None


# ── scrcpy-server.jar ────────────────────────────────────────────

def sync_jar(assets_dir: Path, root_dir: Path) -> None:
    """Ensure assets/scrcpy-server.jar exists (copy from android/ or error)."""
    jar = assets_dir / "scrcpy-server.jar"
    ver = assets_dir / "scrcpy-server.version"
    if jar.is_file():
        log.info("assets/scrcpy-server.jar 已就绪")
        return
    src_jar = root_dir / "android/scrcpy-server.jar"
    src_ver = root_dir / "android/scrcpy-server.version"
    if src_jar.is_file():
        log.info("同步 jar → assets/")
        shutil.copy2(src_jar, jar)
        if src_ver.is_file():
            shutil.copy2(src_ver, ver)
    else:
        log.error("assets/scrcpy-server.jar 与 android/scrcpy-server.jar 均不存在\n请先运行: bash scripts/build-server.sh")


# ── Combined sync (jar + models) ─────────────────────────────────

def sync_all(assets_dir: Path, root_dir: Path) -> None:
    """Ensure scrcpy jar + OCR models are ready for embed (build.sh sync_assets)."""
    assets_dir.mkdir(parents=True, exist_ok=True)
    (assets_dir / "ocr").mkdir(parents=True, exist_ok=True)
    sync_jar(assets_dir, root_dir)
    sync_models(assets_dir / "ocr")


# ── Helpers ──────────────────────────────────────────────────────

def _nonempty(p: Path) -> bool:
    """True if p exists and is non-empty (shell's `-s` test). Single stat."""
    try:
        return p.stat().st_size > 0
    except OSError:
        return False



def _assets_dir() -> Path:
    """The assets/ocr directory."""
    return Path(__file__).resolve().parent.parent.parent / "assets" / "ocr"


def _system_lib_paths(target: Target) -> list[str]:
    """Candidate system paths for the ORT lib (host target only)."""
    name = target.lib_name
    if target.goos == "darwin":
        return [
            f"/opt/homebrew/lib/{name}",
            f"/usr/local/lib/{name}",
            f"/opt/homebrew/Cellar/onnxruntime/*/lib/{name}",
            f"/usr/lib/{name}",
        ]
    if target.goos == "linux":
        return [f"/usr/local/lib/{name}", f"/usr/lib/{name}", f"/lib/{name}"]
    return []
