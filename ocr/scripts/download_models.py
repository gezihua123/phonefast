#!/usr/bin/env python3
"""Download ONNX OCR assets — PP-OCR v3 models + ONNX Runtime shared library.

Usage:
  python3 scripts/download_models.py                     # models + host runtime lib
  python3 scripts/download_models.py --models             # only models (platform-independent)
  python3 scripts/download_models.py --lib                # only host runtime lib
  python3 scripts/download_models.py --lib --target darwin/arm64  # cross fetch
  python3 scripts/download_models.py --lib --force        # re-fetch even if present

Standard library only.
"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

SCRIPTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS_DIR))

from pfbuild import log, assets
from pfbuild.platform import resolve


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="download_models.py",
        description="Download ONNX OCR assets (PP-OCR models + ONNX Runtime lib)",
    )
    parser.add_argument("--models", action="store_true", help="Only download PP-OCR models")
    parser.add_argument("--lib", action="store_true", help="Only download ORT runtime lib")
    parser.add_argument("--target", default="host", help="Target platform goos/goarch (e.g. darwin/arm64)")
    parser.add_argument("--force", action="store_true", help="Re-fetch even if lib present")
    args = parser.parse_args()

    assets_dir = Path(__file__).resolve().parent.parent / "assets" / "ocr"
    assets_dir.mkdir(parents=True, exist_ok=True)

    # If no explicit --models/--lib given, do both (host).
    if not args.models and not args.lib:
        do_models = do_lib = True
    else:
        do_models, do_lib = args.models, args.lib

    if do_models:
        assets.sync_models(assets_dir)

    if do_lib:
        target = resolve(args.target or "host")
        assets.sync_lib(target, force=args.force)

    log.info(f"Done! ONNX OCR assets ready in {assets_dir}")
    log.info("Now run: python3 scripts/build.py")


if __name__ == "__main__":
    main()
