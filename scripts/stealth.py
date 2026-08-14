#!/usr/bin/env python3
"""
Anti-detection configuration for the vendored google_play_scraper.

This is a pure config module — no monkey-patching.  The vendored library
at vendor/google_play_scraper/ imports from here directly:

  - utils/request.py   → uses build_headers(), new_client(), stealth_sleep()
  - features/reviews.py → uses stealth_sleep() for pagination delays

Environment switches:
  GP_SCRAPER_NO_STEALTH=1   disable stealth entirely (plain urllib fallback)
  GP_PROXY=<url>            route all traffic through this proxy
  GP_UA_FIXED=<ua string>   pin a single User-Agent (no rotation)
"""

from __future__ import annotations

import os
import random
import time
from typing import Any

import httpx

# ── switches ───────────────────────────────────────────────────────────

def stealth_enabled() -> bool:
    return os.environ.get("GP_SCRAPER_NO_STEALTH") != "1"


# ── User-Agent pool ────────────────────────────────────────────────────

_BROWSER_UAS = [
    # Chrome 131 – Windows 11
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
    # Chrome 131 – macOS
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
    # Chrome 130 – Windows 10
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
    # Edge 131 – Windows 11
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
    # Firefox 133 – Windows
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) "
    "Gecko/20100101 Firefox/133.0",
    # Firefox 133 – macOS
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) "
    "Gecko/20100101 Firefox/133.0",
    # Chrome 130 – Android Pixel
    "Mozilla/5.0 (Linux; Android 14; Pixel 8 Pro) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36",
]

_ACCEPT_LANGUAGES = [
    "en-US,en;q=0.9",
    "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7",
    "en-GB,en;q=0.9,en-US;q=0.8",
]


def pick_user_agent() -> str:
    """Return a browser User-Agent.  Pinned value wins, else random."""
    fixed = os.environ.get("GP_UA_FIXED")
    if fixed:
        return fixed
    if not stealth_enabled():
        return _BROWSER_UAS[0]
    return random.choice(_BROWSER_UAS)


def build_headers(extra: dict[str, str] | None = None) -> dict[str, str]:
    """Construct a realistic browser header set."""
    headers = {
        "User-Agent": pick_user_agent(),
        "Accept": ("text/html,application/xhtml+xml,application/xml;q=0.9,"
                   "image/avif,image/webp,*/*;q=0.8"),
        "Accept-Language": random.choice(_ACCEPT_LANGUAGES),
        "Accept-Encoding": "gzip, deflate, br",
        "Cache-Control": "no-cache",
        "Pragma": "no-cache",
        "Sec-Fetch-Dest": "empty",
        "Sec-Fetch-Mode": "no-cors",
        "Sec-Fetch-Site": "none",
    }
    if extra:
        headers.update(extra)
    return headers


# ── proxy ──────────────────────────────────────────────────────────────

_proxy_url: str | None = (
    os.environ.get("GP_PROXY")
    or os.environ.get("HTTPS_PROXY")
    or os.environ.get("HTTP_PROXY")
    or None
)


def set_proxy(url: str | None):
    global _proxy_url
    _proxy_url = url
    reset_client()


def get_proxy() -> str | None:
    return _proxy_url


# ── shared httpx client ────────────────────────────────────────────────

_client: httpx.Client | None = None


def new_client() -> httpx.Client:
    """Create (or reuse) a configured httpx client."""
    global _client
    if _client is None:
        transport_kwargs: dict[str, Any] = {"retries": 2, "http2": True}
        if _proxy_url:
            transport_kwargs["proxy"] = _proxy_url
        _client = httpx.Client(
            timeout=httpx.Timeout(30.0, connect=15.0),
            limits=httpx.Limits(max_keepalive_connections=5,
                                max_connections=10),
            transport=httpx.HTTPTransport(**transport_kwargs),
            verify=False,          # match upstream library behaviour
            follow_redirects=True,
        )
    return _client


def reset_client():
    global _client
    if _client is not None:
        _client.close()
    _client = None


# ── timing jitter ──────────────────────────────────────────────────────

def stealth_sleep(base_ms: int) -> float:
    """
    Sleep base_ms ±40 % jitter.  With stealth disabled this is a plain
    fixed sleep.  Returns actual seconds slept.
    """
    if not stealth_enabled():
        time.sleep(base_ms / 1000)
        return base_ms / 1000

    jitter = random.uniform(0.6, 1.6)
    actual_ms = max(50, base_ms * jitter)
    time.sleep(actual_ms / 1000)
    return actual_ms / 1000


def randomized_count(count: int) -> int:
    """
    Vary a per-page fetch count ±15 % so consecutive pages don't all
    request identical batch sizes.  Caller must clamp afterwards.
    """
    if not stealth_enabled() or count <= 10:
        return count
    return max(5, int(count * random.uniform(0.85, 1.15)))
