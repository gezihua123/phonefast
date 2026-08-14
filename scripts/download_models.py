#!/usr/bin/env python3
"""Download ONNX OCR assets — PP-OCR models + ONNX Runtime shared library.

Thin delegate: the real implementation lives in the OCR module
(ocr/scripts/download_models.py) and writes into ocr/assets/. This wrapper
exists because CI/workflows/docs call `python3 scripts/download_models.py`
(via scripts/download-ocr-models.sh) — keeping that path working without
duplicating the download logic. All arguments are forwarded verbatim:
--models / --lib / --keys / --level / --target / --force.
"""
from __future__ import annotations

import os
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
IMPL = ROOT / "ocr" / "scripts" / "download_models.py"


def main() -> None:
    if not IMPL.is_file():
        sys.stderr.write(f"error: OCR downloader not found at {IMPL}\n")
        sys.exit(1)
    # Exec the module-local implementation, replacing this process so the
    # exit code and any download output propagate unchanged to the caller.
    os.execvp(sys.executable, [sys.executable, str(IMPL), *sys.argv[1:]])


if __name__ == "__main__":
    main()
