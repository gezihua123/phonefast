#!/usr/bin/env python3
"""Download PP-OCR test model variants (v3 backup, v4 mobile) for eval tests.

These are NOT the production models (those live in assets/ocr/ and are embedded);
these are extra variants kept for historical/comparison benchmarking.

Usage:
  python3 scripts/download_test_models.py          # v3 + v4 mobile
  python3 scripts/download_test_models.py --v3     # v3 only
  python3 scripts/download_test_models.py --v4     # v4 mobile only

Standard library only.
"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

SCRIPTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS_DIR))

from pfbuild import log, assets


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="download_test_models.py",
        description="Download PP-OCR test model variants (v3/v4) for eval",
    )
    parser.add_argument("--v3", action="store_true", help="Only v3")
    parser.add_argument("--v4", action="store_true", help="Only v4 mobile")
    args = parser.parse_args()

    dest = Path(__file__).resolve().parent.parent / "tests" / "ocr-models"
    dest.mkdir(parents=True, exist_ok=True)

    hf = "https://huggingface.co/SWHL/RapidOCR/resolve/main"

    if args.v3 or (not args.v3 and not args.v4):
        log.info("v3 (PP-OCRv3, same as production assets/ocr):")
        assets.http_get(f"{hf}/PP-OCRv3/ch_PP-OCRv3_det_infer.onnx", dest / "v3_det.onnx")
        assets.http_get(f"{hf}/PP-OCRv3/ch_PP-OCRv3_rec_infer.onnx", dest / "v3_rec.onnx")
        log.info(f"  v3_det.onnx {log.human_size(dest / 'v3_det.onnx')}")
        log.info(f"  v3_rec.onnx {log.human_size(dest / 'v3_rec.onnx')}")

    if args.v4 or (not args.v3 and not args.v4):
        log.info("v4 mobile (PP-OCRv4 mobile, evaluated & not adopted — see docs/DEV.md):")
        assets.http_get(f"{hf}/PP-OCRv4/ch_PP-OCRv4_det_infer.onnx", dest / "v4_mobile_det.onnx")
        assets.http_get(f"{hf}/PP-OCRv4/ch_PP-OCRv4_rec_infer.onnx", dest / "v4_mobile_rec.onnx")
        log.info(f"  v4_mobile_det.onnx {log.human_size(dest / 'v4_mobile_det.onnx')}")
        log.info(f"  v4_mobile_rec.onnx {log.human_size(dest / 'v4_mobile_rec.onnx')}")

    log.info(f"Done. Models in {dest}")


if __name__ == "__main__":
    main()
