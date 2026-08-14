#!/usr/bin/env python3
"""Download PP-OCR test model variants (v3 backup, v4 mobile) for eval tests.

These are NOT the production models (those live in ocr/assets/ and are embedded
into every plain/cgo1/full build); these are extra variants kept in ocr/models/
for historical/comparison benchmarking.

Usage:
  python3 ocr/scripts/download_test_models.py          # v3 + v4 mobile
  python3 ocr/scripts/download_test_models.py --v3     # v3 only
  python3 ocr/scripts/download_test_models.py --v4     # v4 mobile only

Standard library only (no pfbuild dependency).
"""
from __future__ import annotations

import argparse
import urllib.request
from pathlib import Path


def _human_size(p: Path) -> str:
    n = p.stat().st_size if p.is_file() else 0
    for unit in ("B", "KB", "MB", "GB"):
        if n < 1024:
            return f"{n:.0f} {unit}"
        n /= 1024
    return f"{n:.0f} TB"


def _http_get(url: str, dest: Path) -> None:
    print(f"  fetching {url}")
    urllib.request.urlretrieve(url, dest)


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="download_test_models.py",
        description="Download PP-OCR test model variants (v3/v4) for eval",
    )
    parser.add_argument("--v3", action="store_true", help="Only v3")
    parser.add_argument("--v4", action="store_true", help="Only v4 mobile")
    args = parser.parse_args()

    # ocr/scripts/ -> ocr/models/
    dest = Path(__file__).resolve().parent.parent / "models"
    dest.mkdir(parents=True, exist_ok=True)

    hf = "https://huggingface.co/SWHL/RapidOCR/resolve/main"

    if args.v3 or (not args.v3 and not args.v4):
        print("v3 (PP-OCRv3, same as production ocr/assets):")
        _http_get(f"{hf}/PP-OCRv3/ch_PP-OCRv3_det_infer.onnx", dest / "v3_det.onnx")
        _http_get(f"{hf}/PP-OCRv3/ch_PP-OCRv3_rec_infer.onnx", dest / "v3_rec.onnx")
        print(f"  v3_det.onnx {_human_size(dest / 'v3_det.onnx')}")
        print(f"  v3_rec.onnx {_human_size(dest / 'v3_rec.onnx')}")

    if args.v4 or (not args.v3 and not args.v4):
        print("v4 mobile (PP-OCRv4 mobile, evaluated & not adopted - see docs/DEV.md):")
        _http_get(f"{hf}/PP-OCRv4/ch_PP-OCRv4_det_infer.onnx", dest / "v4_mobile_det.onnx")
        _http_get(f"{hf}/PP-OCRv4/ch_PP-OCRv4_rec_infer.onnx", dest / "v4_mobile_rec.onnx")
        print(f"  v4_mobile_det.onnx {_human_size(dest / 'v4_mobile_det.onnx')}")
        print(f"  v4_mobile_rec.onnx {_human_size(dest / 'v4_mobile_rec.onnx')}")

    print(f"Done. Models in {dest}")


if __name__ == "__main__":
    main()
