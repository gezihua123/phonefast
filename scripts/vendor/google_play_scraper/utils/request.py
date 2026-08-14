"""
Vendored HTTP layer — modified to route through httpx with stealth config.

Upstream used urllib + default Python headers (trivially fingerprinted).
This version delegates header construction, proxying and timing jitter
to the project-level `stealth` module.

When GP_SCRAPER_NO_STEALTH=1 is set, a plain urllib fallback kicks in
and behaviour is identical to upstream.
"""

import os
import ssl
import time
from typing import Union
from urllib.error import HTTPError
from urllib.request import Request, urlopen

# Project-level stealth config.  scraper.py puts the project root on
# sys.path before importing this package, so `import stealth` resolves
# to <project>/stealth.py.
import stealth

from google_play_scraper.exceptions import ExtraHTTPError, NotFoundError

ssl._create_default_https_context = ssl._create_unverified_context

MAX_RETRIES = 3
RATE_LIMIT_DELAY = 5

_STEALTH_OFF = os.environ.get("GP_SCRAPER_NO_STEALTH") == "1"


# ── plain-urllib fallback (stealth disabled — identical to upstream) ──

def _urlopen_plain(obj) -> str:
    try:
        resp = urlopen(obj)
    except HTTPError as e:
        if e.code == 404:
            raise NotFoundError("App not found(404).")
        raise ExtraHTTPError(
            "App not found. Status code {} returned.".format(e.code)
        )
    return resp.read().decode("UTF-8")


def _post_plain(url: str, data: Union[str, bytes], headers: dict) -> str:
    last_exception = None
    rate_exceeded_count = 0
    for _ in range(MAX_RETRIES):
        try:
            resp = _urlopen_plain(Request(url, data=data, headers=headers))
        except Exception as e:
            last_exception = e
            continue
        if "com.google.play.gateway.proto.PlayGatewayError" in resp:
            rate_exceeded_count += 1
            last_exception = Exception("com.google.play.gateway.proto.PlayGatewayError")
            time.sleep(RATE_LIMIT_DELAY * rate_exceeded_count)
            continue
        return resp
    raise last_exception


# ── stealth path (default) ────────────────────────────────────────────

def _post_stealth(url: str, data: Union[str, bytes], headers: dict) -> str:
    client = stealth.new_client()
    merged = stealth.build_headers(headers)

    last_exception = None
    rate_exceeded_count = 0

    for _ in range(MAX_RETRIES):
        try:
            resp = client.post(url, content=data, headers=merged)
            if resp.status_code == 404:
                raise NotFoundError("App not found(404).")
            if resp.status_code >= 400:
                raise ExtraHTTPError(
                    "App not found. Status code {} returned.".format(resp.status_code)
                )
        except (NotFoundError, ExtraHTTPError):
            raise
        except Exception as e:
            last_exception = e
            continue

        text = resp.text
        if "com.google.play.gateway.proto.PlayGatewayError" in text:
            rate_exceeded_count += 1
            last_exception = Exception("com.google.play.gateway.proto.PlayGatewayError")
            # jittered backoff — upstream slept a fixed 5s * count
            stealth.stealth_sleep(int(RATE_LIMIT_DELAY * rate_exceeded_count * 1000))
            stealth.reset_client()          # fresh connection after throttle
            continue

        return text

    raise last_exception


def _urlopen_stealth(obj) -> str:
    """GET path — used by app / search features."""
    client = stealth.new_client()
    url = obj.full_url if hasattr(obj, "full_url") else str(obj)
    resp = client.get(url, headers=stealth.build_headers())
    if resp.status_code == 404:
        raise NotFoundError("App not found(404).")
    if resp.status_code >= 400:
        raise ExtraHTTPError(
            "App not found. Status code {} returned.".format(resp.status_code)
        )
    return resp.text


# ── public API (same names as upstream) ───────────────────────────────

def post(url: str, data: Union[str, bytes], headers: dict) -> str:
    if _STEALTH_OFF:
        return _post_plain(url, data, headers)
    return _post_stealth(url, data, headers)


def get(url: str) -> str:
    if _STEALTH_OFF:
        return _urlopen_plain(url)
    return _urlopen_stealth(url)
