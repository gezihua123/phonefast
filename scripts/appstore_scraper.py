#!/usr/bin/env python3
"""
Apple App Store Reviews Scraper
================================

Uses Apple's official public RSS endpoint (no auth required):

    https://itunes.apple.com/{country}/rss/customerreviews/id={trackId}/json

Limitations of the official endpoint (verified 2026-07-30):
  - ~50 most recent reviews per country per sort order
  - Old page=N pagination is deprecated (returns empty)
  - Combine multiple countries for wider coverage (~300-500 recent)
  - No developer replies exposed via RSS

Output fields are aligned with the Google Play scraper output so both
platforms can be analysed together (plus App Store specific `title`,
`country`, `platform`).

Usage:
  python3 scripts/appstore_scraper.py "co-star"                  # search & scrape
  python3 scripts/appstore_scraper.py 1264782561                 # direct trackId
  python3 scripts/appstore_scraper.py 1264782561 --countries us,jp,gb,ca,au
  python3 scripts/appstore_scraper.py 1264782561 --sort mosthelpful
"""

from __future__ import annotations

import argparse
import csv
import json
import sys
import time
from datetime import datetime
from pathlib import Path

# ── path setup: project scripts dir + vendored deps ──────────────────
_SCRIPTS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(_SCRIPTS_DIR))
sys.path.insert(0, str(_SCRIPTS_DIR / "vendor"))

import stealth  # noqa: E402

# ── config ────────────────────────────────────────────────────────────
DEFAULT_OUT_DIR = _SCRIPTS_DIR.parent / "output" / "appstore"

# Countries worth polling by default (major App Store storefronts)
DEFAULT_COUNTRIES = ["us", "gb", "ca", "au", "jp", "de", "fr", "kr"]

SORT_MAP = {
    "mostrecent": "mostrecent",
    "mosthelpful": "mosthelpful",
}


# ── iTunes Search API: name → trackId ────────────────────────────────

def search_app(term: str, country: str = "us", limit: int = 5) -> list[dict]:
    """Resolve an app name to candidate trackIds via iTunes Search API."""
    from urllib.parse import quote
    client = stealth.new_client()
    url = (f"https://itunes.apple.com/search?term={quote(term)}"
           f"&entity=software&country={country}&limit={limit}")
    resp = client.get(url, headers=stealth.build_headers())
    resp.raise_for_status()
    results = resp.json().get("results", [])
    return [
        {
            "trackId": r["trackId"],
            "trackName": r["trackName"],
            "sellerName": r.get("sellerName", ""),
            "averageUserRating": r.get("averageUserRating"),
            "userRatingCount": r.get("userRatingCount"),
        }
        for r in results
    ]


# ── RSS review fetch ──────────────────────────────────────────────────

def fetch_reviews_rss(track_id: int, country: str, sort: str = "mostrecent") -> list[dict]:
    """Fetch the latest reviews for one country via the official RSS feed."""
    client = stealth.new_client()
    url = (f"https://itunes.apple.com/{country}/rss/customerreviews"
           f"/id={track_id}/sortby={sort}/json")
    resp = client.get(url, headers=stealth.build_headers())
    if resp.status_code != 200:
        return []
    data = resp.json()
    entries = data.get("feed", {}).get("entry", [])

    reviews = []
    for e in entries:
        # The RSS feed sometimes includes an app-metadata entry first;
        # real review entries always have im:rating.
        if "im:rating" not in e:
            continue
        reviews.append({
            "reviewId": str(e.get("id", {}).get("label", "")),
            "userName": e.get("author", {}).get("name", {}).get("label", ""),
            "userImage": "",                     # not exposed by RSS
            "title": e.get("title", {}).get("label", ""),
            "content": (e.get("content", {}).get("label", "")
                        .replace("\n", " ").replace("\r", "")),
            "score": int(e["im:rating"]["label"]),
            "thumbsUpCount": int(e.get("im:voteCount", {}).get("label", 0)),
            "at": e.get("updated", {}).get("label", ""),
            "replyContent": "",                  # not exposed by RSS
            "replyDate": "",
            "appVersion": e.get("im:version", {}).get("label", ""),
            "country": country,
            "platform": "ios",
        })
    return reviews


# ── output ────────────────────────────────────────────────────────────

def flatten_for_output(reviews: list[dict], app_slug: str) -> list[dict]:
    """Attach a canonical reviewUrl pointing at the App Store page."""
    url = f"https://apps.apple.com/app/id{app_slug}"
    return [{**r, "reviewUrl": url} for r in reviews]


def save_output(reviews: list[dict], track_id: int, app_name: str,
                fmt: str, out_dir: Path) -> None:
    safe_name = "".join(c if c.isalnum() or c in "._-" else "_"
                        for c in app_name)[:40]
    app_dir = out_dir / f"{track_id}_{safe_name}"
    app_dir.mkdir(parents=True, exist_ok=True)
    ts = datetime.now().strftime("%Y%m%d_%H%M%S")
    stem = f"{track_id}_{ts}"

    if fmt in ("csv", "both"):
        csv_path = app_dir / f"{stem}.csv"
        fieldnames = list(reviews[0].keys())
        with open(csv_path, "w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames, lineterminator="\n")
            writer.writeheader()
            writer.writerows(reviews)
        print(f"📄 CSV → {csv_path}")

    if fmt in ("json", "both"):
        json_path = app_dir / f"{stem}.json"
        with open(json_path, "w", encoding="utf-8") as f:
            json.dump(reviews, f, ensure_ascii=False, indent=2)
            f.write("\n")
        print(f"📄 JSON → {json_path}")


# ── main ──────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Scrape Apple App Store reviews via official RSS endpoint",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument("app", help="App Store numeric trackId, or app name to search")
    parser.add_argument("--countries", default=",".join(DEFAULT_COUNTRIES),
                        help=f"Comma-separated storefront codes "
                             f"(default: {','.join(DEFAULT_COUNTRIES)})")
    parser.add_argument("--sort", default="mostrecent",
                        choices=list(SORT_MAP.keys()),
                        help="Sort order (default: mostrecent)")
    parser.add_argument("-f", "--format", default="both",
                        choices=["csv", "json", "both"],
                        help="Output format (default: both)")
    parser.add_argument("-o", "--outdir", default=str(DEFAULT_OUT_DIR),
                        help="Output directory")
    parser.add_argument("--sleep", type=int, default=1200,
                        help="Base sleep ms between country requests (default: 1200; "
                             "Apple is more rate-limit sensitive than Google)")
    parser.add_argument("--pick", type=int, default=0,
                        help="Search result index to use (default: 0 = top hit)")
    parser.add_argument("--proxy", type=str, metavar="URL",
                        help="Proxy URL (or set GP_PROXY env var)")
    args = parser.parse_args()

    if args.proxy:
        stealth.set_proxy(args.proxy)
        print(f"🔀 proxy: {args.proxy}")

    # Resolve trackId
    if args.app.isdigit():
        track_id = int(args.app)
        app_name = ""
        # fetch app name for the folder label
        client = stealth.new_client()
        r = client.get(f"https://itunes.apple.com/lookup?id={track_id}",
                       headers=stealth.build_headers())
        results = r.json().get("results", [])
        if results:
            app_name = results[0].get("trackName", "")
        if not app_name:
            print(f"❌ trackId {track_id} not found")
            sys.exit(1)
    else:
        print(f"🔍 Searching App Store for: {args.app}")
        candidates = search_app(args.app)
        if not candidates:
            print("❌ No apps found")
            sys.exit(1)
        for i, c in enumerate(candidates):
            rating = f"⭐{c['averageUserRating']:.2f}" if c["averageUserRating"] else "  -  "
            print(f"  [{i}] {c['trackId']} | {c['trackName']} | "
                  f"{rating} ({c['userRatingCount']} ratings)")
        chosen = candidates[args.pick]
        track_id = chosen["trackId"]
        app_name = chosen["trackName"]
        print(f"✅ Using: {app_name} (id={track_id})")

    countries = [c.strip().lower() for c in args.countries.split(",") if c.strip()]
    print(f"\n🌍 Countries: {', '.join(countries)} | sort={args.sort}")

    # Fetch per country
    all_reviews: list[dict] = []
    seen_ids: set[str] = set()
    empty_streak = 0

    for country in countries:
        try:
            reviews = fetch_reviews_rss(track_id, country, sort=args.sort)
        except Exception as e:
            print(f"  ⚠️  {country}: failed ({e})")
            continue

        fresh = 0
        for r in reviews:
            if r["reviewId"] and r["reviewId"] not in seen_ids:
                seen_ids.add(r["reviewId"])
                all_reviews.append(r)
                fresh += 1
        print(f"  {country}: {len(reviews)} fetched, {fresh} new (total {len(all_reviews)})")

        # Apple soft rate-limit detection: HTTP 200 + empty feed.
        # Empty feed is ambiguous (no reviews vs throttled) — a streak of
        # empties across different storefronts strongly suggests throttling.
        if len(reviews) == 0:
            empty_streak += 1
            if empty_streak >= 3:
                print("\n⛔ 3 consecutive empty feeds — likely Apple edge "
                      "soft rate-limit (HTTP 200 with empty body).")
                print("   This is transient; wait 1-5 minutes and retry, "
                      "or route via --proxy / GP_PROXY.")
                print(f"   Keeping {len(all_reviews)} reviews collected so far.")
                break
            # back off harder while the streak grows
            stealth.stealth_sleep(args.sleep * (2 ** empty_streak))
        else:
            empty_streak = 0

        if country != countries[-1]:
            stealth.stealth_sleep(args.sleep)

    if not all_reviews:
        print("\n⚠️  No reviews collected. The app may have no recent reviews "
              "in these storefronts — or you hit Apple's soft rate limit "
              "(empty 200 responses). Retry in a few minutes.")
        sys.exit(0)

    all_reviews = flatten_for_output(all_reviews, str(track_id))
    # newest first
    all_reviews.sort(key=lambda r: r["at"], reverse=True)

    print(f"\n✅ Total unique reviews: {len(all_reviews)}")
    dates = [r["at"][:10] for r in all_reviews if r["at"]]
    if dates:
        print(f"   Date range: {min(dates)} → {max(dates)}")

    save_output(all_reviews, track_id, app_name, args.format, Path(args.outdir))


if __name__ == "__main__":
    main()
