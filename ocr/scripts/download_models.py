#!/usr/bin/env python3
"""Download ONNX OCR assets — PP-OCRv6 models + ONNX Runtime shared library.

Usage:
  python3 scripts/download_models.py                     # models + host runtime lib
  python3 scripts/download_models.py --models             # only models (platform-independent)
  python3 scripts/download_models.py --lib                # only host runtime lib
  python3 scripts/download_models.py --keys               # only character set
  python3 scripts/download_models.py --level small        # model level (medium/small/tiny)

Standard library + yaml (for keys extraction).
"""
from __future__ import annotations

import argparse
import os
import sys
import urllib.request
import json
from pathlib import Path


def log_info(msg: str) -> None:
    print(f"[+] {msg}")


def log_warn(msg: str) -> None:
    print(f"[!] {msg}")


def log_error(msg: str) -> None:
    print(f"[x] {msg}", file=sys.stderr)
    sys.exit(1)


def download(url: str, dest: Path) -> None:
    """Download a file with retries."""
    import time
    for attempt in range(3):
        try:
            urllib.request.urlretrieve(url, str(dest))
            return
        except Exception as e:
            if attempt == 2:
                raise
            time.sleep(1)


def download_models(assets_dir: Path, level: str = "medium") -> None:
    """Download PP-OCRv6 ONNX models from HuggingFace."""
    det = assets_dir / "ppocr-det.onnx"
    rec = assets_dir / "ppocr-rec.onnx"

    if det.exists() and rec.exists():
        log_info("Models already present")
        return

    assets_dir.mkdir(parents=True, exist_ok=True)

    hf = f"https://huggingface.co/PaddlePaddle/PP-OCRv6_{level}_det_onnx/resolve/main/inference.onnx"
    hf_rec = f"https://huggingface.co/PaddlePaddle/PP-OCRv6_{level}_rec_onnx/resolve/main/inference.onnx"

    log_info(f"Downloading PP-OCRv6 {level} models...")
    if not det.exists():
        log_info(f"  det: {hf}")
        download(hf, det)
        log_info(f"  det: {det.stat().st_size / 1024 / 1024:.0f}MB")

    if not rec.exists():
        log_info(f"  rec: {hf_rec}")
        download(hf_rec, rec)
        log_info(f"  rec: {rec.stat().st_size / 1024 / 1024:.0f}MB")


def download_keys(keys_dir: Path) -> None:
    """Download PP-OCRv6 character set from model config."""
    keys_file = keys_dir / "ppocr_keys.txt"
    yml_url = "https://huggingface.co/PaddlePaddle/PP-OCRv6_medium_rec_onnx/resolve/main/inference.yml"

    import tempfile
    import yaml

    log_info("Downloading PP-OCRv6 character set...")
    with tempfile.NamedTemporaryFile(suffix=".yml", delete=False) as tmp:
        download(yml_url, Path(tmp.name))
        with open(tmp.name) as f:
            cfg = yaml.safe_load(f)
        chars = cfg["PostProcess"]["character_dict"]
        with open(keys_file, "w") as kf:
            for c in chars:
                kf.write(c + "\n")
        os.unlink(tmp.name)

    log_info(f"Extracted {len(chars)} characters -> {keys_file}")


def download_lib(assets_dir: Path, target: str = "host", force: bool = False) -> None:
    """Download ONNX Runtime library."""
    # Map target to platform
    import platform as pf
    os_name = pf.system().lower()
    arch = pf.machine().lower()

    if target == "host":
        pass  # use os_name/arch from above
    else:
        parts = target.split("/")
        os_name = parts[0]
        arch = parts[1] if len(parts) > 1 else arch

    lib_map = {
        "darwin": "libonnxruntime.dylib",
        "linux": "libonnxruntime.so",
        "windows": "onnxruntime.dll",
    }
    lib_name = lib_map.get(os_name, "libonnxruntime.so")

    # Try system paths first
    system_paths = {
        "darwin": ["/opt/homebrew/lib", "/usr/local/lib"],
        "linux": ["/usr/local/lib", "/usr/lib"],
    }
    for d in system_paths.get(os_name, []):
        p = Path(d) / lib_name
        if p.exists():
            dest = assets_dir / f"libonnxruntime-{os_name}-{arch}.dylib"
            if not dest.exists() or force:
                import shutil
                shutil.copy2(p, dest)
                log_info(f"Copied system ORT: {p} -> {dest}")
                return
            else:
                log_info(f"ORT lib already present: {dest}")
                return

    # Fallback: download from GitHub
    suffix_map = {
        "darwin/arm64": "osx-arm64",
        "darwin/x86_64": "osx-x86_64",
        "linux/x86_64": "linux-x64",
        "linux/aarch64": "linux-aarch64",
    }
    key = f"{os_name}/{arch}"
    suffix = suffix_map.get(key)
    if not suffix:
        log_error(f"No ORT release for {key}")

    ort_version = os.environ.get("ORT_VERSION", "1.27.1")
    pkg = f"onnxruntime-{suffix}-{ort_version}"
    url = f"https://github.com/microsoft/onnxruntime/releases/download/v{ort_version}/{pkg}.tgz"

    import tempfile, tarfile
    log_info(f"Downloading ONNX Runtime {ort_version} ({suffix})...")
    with tempfile.NamedTemporaryFile(suffix=".tgz", delete=False) as tmp:
        download(url, Path(tmp.name))
        with tarfile.open(tmp.name) as tar:
            tar.extractall(path=tmp.name + ".dir")
        lib_path = Path(tmp.name + ".dir") / pkg / "lib" / lib_name
        dest = assets_dir / f"libonnxruntime-{os_name}-{arch}.dylib"
        import shutil
        shutil.copy2(lib_path, dest)
        os.unlink(tmp.name)
        log_info(f"ORT lib: {dest.stat().st_size / 1024 / 1024:.0f}MB")
    log_info("Done!")


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="download_models.py",
        description="Download ONNX OCR assets (PP-OCRv6 models + ONNX Runtime lib)",
    )
    parser.add_argument("--models", action="store_true", help="Only download PP-OCRv6 models")
    parser.add_argument("--lib", action="store_true", help="Only download ORT runtime lib")
    parser.add_argument("--keys", action="store_true", help="Only download character set")
    parser.add_argument("--level", default="medium", choices=["medium", "small", "tiny"],
                        help="Model level (default: medium)")
    parser.add_argument("--target", default="host", help="Target platform (e.g. darwin/arm64)")
    parser.add_argument("--force", action="store_true", help="Re-fetch even if present")
    args = parser.parse_args()

    assets_dir = Path(__file__).resolve().parent.parent / "assets"
    keys_dir = Path(__file__).resolve().parent.parent / "common"

    # Default: do all
    if not args.models and not args.lib and not args.keys:
        do_models = do_lib = do_keys = True
    else:
        do_models, do_lib, do_keys = args.models, args.lib, args.keys

    if do_models:
        download_models(assets_dir, args.level)

    if do_lib:
        download_lib(assets_dir, args.target, args.force)

    if do_keys:
        download_keys(keys_dir)

    log_info("All done!")


if __name__ == "__main__":
    main()