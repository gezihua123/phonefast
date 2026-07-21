"""Unified colored logging — replaces the RED/GREEN/YELLOW helpers scattered
across build.sh and download-ocr-models.sh."""
import sys
from pathlib import Path

_GREEN = "\033[0;32m"
_YELLOW = "\033[1;33m"
_RED = "\033[0;31m"
_NC = "\033[0m"


def info(msg: str) -> None:
    """Green [+] — progress / success (build.sh info / download log)."""
    print(f"{_GREEN}[+]{_NC} {msg}")


def warn(msg: str) -> None:
    """Yellow [!] — non-fatal warning (unified; build.sh used yellow, download used red)."""
    print(f"{_YELLOW}[!]{_NC} {msg}")


def error(msg: str) -> None:
    """Red [x] + exit 1 — fatal (build.sh error / download's warn+exit)."""
    print(f"{_RED}[x]{_NC} {msg}", file=sys.stderr)
    sys.exit(1)


def human_size(p: Path) -> str:
    """Human-readable file size (du -h style, via os.stat — no subprocess)."""
    size = p.stat().st_size
    for unit in ("B", "K", "M", "G"):
        if size < 1024:
            return f"{size:.0f}{unit}" if unit == "B" else f"{size:.1f}{unit}"
        size /= 1024
    return f"{size:.1f}T"

