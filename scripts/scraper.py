#!/usr/bin/env python3
"""
Google Play Reviews Scraper
Usage: python scraper.py [app_id] [options]

NOTE on PII: Google Play reviews API does NOT return user email, phone,
or any personal identifiers.  Only userName (public display name) and
userImage (avatar URL) are available.  Developer reply emails are
public support addresses, not user PII.
"""

import argparse
import csv
import json
import sys
import time
from datetime import datetime
from pathlib import Path

# Use the vendored (stealth-patched) copy of google_play_scraper.
# vendor/ must be on sys.path BEFORE the site-packages version.
_PROJECT_ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(_PROJECT_ROOT))              # for `import stealth`
sys.path.insert(0, str(_PROJECT_ROOT / "vendor"))   # vendored library first

import stealth  # noqa: E402  (anti-detection config; pure module, no patching)
from google_play_scraper import Sort, reviews, reviews_all  # noqa: E402

# ── config ───────────────────────────────────────────────────────────
DEFAULT_APP_ID = "com.costarastrology"
DEFAULT_LANG = "en"
DEFAULT_COUNTRY = "us"
DEFAULT_SORT = Sort.NEWEST
DEFAULT_COUNT = 500       # how many to fetch per run; set 0 for "all"
# Anchor to project root (one level up from scripts/), so output lands in
# <project>/output regardless of the directory you launch from.
DEFAULT_OUT_DIR = _PROJECT_ROOT.parent / "output"
DEFAULT_MAX_RETRIES = 3   # max retry attempts for transient failures

SORT_MAP = {
    "newest": Sort.NEWEST,
    "relevant": Sort.MOST_RELEVANT,
    "rating": Sort.RATING,
}

# ── fetch helpers ─────────────────────────────────────────────────────

def _paginated_fetch(
    app_id: str,
    lang: str,
    country: str,
    sort: Sort,
    count: int,
    filter_score: int | None,
    sleep_ms: int,
    max_retries: int,
) -> list[dict]:
    """
    Fetch exactly `count` reviews by paginating through continuation tokens.
    Uses reviews() in a loop rather than reviews_all(), because reviews_all()
    ignores the count parameter and returns everything.
    """
    collected: list[dict] = []
    token = None
    page = 0

    while len(collected) < count:
        page += 1
        remaining = count - len(collected)
        batch_count = min(remaining, 200)  # API max per page

        for attempt in range(1, max_retries + 1):
            try:
                result, token = reviews(
                    app_id,
                    lang=lang,
                    country=country,
                    sort=sort,
                    count=batch_count,
                    filter_score_with=filter_score,
                    continuation_token=token,
                )
                if not token:
                    token = None
                break
            except Exception as e:
                if attempt < max_retries:
                    wait = 2 ** attempt
                    print(f"  ⚠  Retry {attempt}/{max_retries} (page {page}): {e} — waiting {wait}s")
                    time.sleep(wait)
                else:
                    print(f"  ❌ Failed after {max_retries} retries on page {page}: {e}")
                    raise

        if not result:
            print(f"  ℹ  No more reviews available (got {len(collected)} of {count})")
            break

        collected.extend(result)
        if len(collected) % 2500 == 0 or len(collected) >= count:
            print(f"  📊 Progress: {len(collected):,} / {count:,} reviews")

        if token is None:
            print(f"  ℹ  End of reviews (got {len(collected)} of {count})")
            break

        time.sleep(sleep_ms / 1000)

    return collected


def fetch_reviews(
    app_id: str,
    lang: str = DEFAULT_LANG,
    country: str = DEFAULT_COUNTRY,
    sort: Sort = DEFAULT_SORT,
    count: int = DEFAULT_COUNT,
    filter_score: int | None = None,
    sleep_ms: int = 100,
    max_retries: int = DEFAULT_MAX_RETRIES,
) -> list[dict]:
    """
    Fetch reviews.

    When count=0 → fetch all via reviews_all().
    When count>0 → paginate with reviews() + continuation_token,
    respecting the exact count (reviews_all ignores count).
    """
    print(f"🔍 Fetching reviews for {app_id}")
    print(f"   lang={lang}  country={country}  sort={sort}"
          f"  count={count or 'ALL'}  score={filter_score or 'any'}")

    for attempt in range(1, max_retries + 1):
        try:
            if count == 0:
                data = reviews_all(
                    app_id,
                    lang=lang,
                    country=country,
                    sort=sort,
                    filter_score_with=filter_score,
                    sleep_milliseconds=sleep_ms,
                )
            else:
                data = _paginated_fetch(
                    app_id=app_id,
                    lang=lang,
                    country=country,
                    sort=sort,
                    count=count,
                    filter_score=filter_score,
                    sleep_ms=sleep_ms,
                    max_retries=max_retries,
                )
            break
        except Exception as e:
            if attempt < max_retries:
                wait = 2 ** attempt
                print(f"  ⚠  Retry {attempt}/{max_retries}: {e} — waiting {wait}s")
                time.sleep(wait)
            else:
                print(f"  ❌ Failed after {max_retries} retries: {e}")
                raise

    print(f"✅ Fetched {len(data)} reviews")
    return data


# ── data processing ───────────────────────────────────────────────────

def flatten_review(r: dict, app_id: str) -> dict:
    """Keep the most useful fields for analysis."""
    return {
        "reviewId": r.get("reviewId"),
        "userImage": r.get("userImage") or "",
        "userName": r.get("userName"),
        "content": (r.get("content") or "").replace("\n", " ").replace("\r", ""),
        "score": r.get("score"),
        "thumbsUpCount": r.get("thumbsUpCount"),
        "at": str(r.get("at")) if r.get("at") else "",
        "replyContent": (r.get("replyContent") or "").replace("\n", " "),
        "replyDate": str(r.get("repliedAt")) if r.get("repliedAt") else "",
        "appVersion": r.get("reviewCreatedVersion") or "",
        "reviewUrl": f"https://play.google.com/store/apps/details?id={app_id}",
    }


def save_output(data: list[dict], app_id: str, fmt: str, out_dir: Path,
                score: int | None = None):
    """Save to csv / json / both.  Automatically wraps with app_id/score directory."""
    score_dir = f"{score}star" if score else "all"
    out_dir = out_dir / app_id / score_dir
    out_dir.mkdir(parents=True, exist_ok=True)
    ts = datetime.now().strftime("%Y%m%d_%H%M%S")
    stem = f"{app_id}_{score_dir}_{ts}"

    flat = [flatten_review(r, app_id) for r in data]

    if fmt in ("csv", "both"):
        csv_path = out_dir / f"{stem}.csv"
        fieldnames = list(flat[0].keys())
        with open(csv_path, "w", newline="", encoding="utf-8") as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames, lineterminator="\n")
            writer.writeheader()
            writer.writerows(flat)
        print(f"📄 CSV → {csv_path}")

    if fmt in ("json", "both"):
        json_path = out_dir / f"{stem}.json"
        with open(json_path, "w", encoding="utf-8") as f:
            json.dump(flat, f, ensure_ascii=False, indent=2)
            f.write("\n")
        print(f"📄 JSON → {json_path}")


# ── batch mode ────────────────────────────────────────────────────────

def load_app_list(path: str) -> list[str]:
    """Load app IDs from a file, one per line. Supports # comments."""
    app_ids = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if line and not line.startswith("#"):
                app_ids.append(line)
    return app_ids


def run_single(args, app_id: str, score: int | None = None) -> dict:
    """Fetch and save reviews for one app_id + score combination."""
    try:
        data = fetch_reviews(
            app_id=app_id,
            lang=args.lang,
            country=args.country,
            sort=SORT_MAP[args.sort],
            count=args.count,
            filter_score=score,
            sleep_ms=args.sleep,
            max_retries=args.max_retries,
        )
        if data:
            save_output(data, app_id, args.format, Path(args.outdir), score=score)
        return {"app_id": app_id, "score": score, "count": len(data), "ok": True}
    except Exception as e:
        print(f"❌ Error fetching {app_id} (score={score}): {e}")
        return {"app_id": app_id, "score": score, "count": 0, "ok": False,
                "error": str(e)}


def run_batch(args) -> list[dict]:
    """Process multiple app_ids and optional multi-star levels."""
    app_ids = load_app_list(args.batch)
    stars = [int(s) for s in args.stars.split(",")] if args.stars else [args.score]

    results: list[dict] = []

    for app_id in app_ids:
        for star in stars:
            print(f"\n{'='*60}")
            print(f"📱 {app_id}  ⭐ {star}star")
            print(f"{'='*60}")
            result = run_single(args, app_id, score=star)
            results.append(result)

    # Summary
    print(f"\n{'='*60}")
    print("📊 BATCH SUMMARY")
    print(f"{'='*60}")
    total = sum(r["count"] for r in results)
    ok = sum(1 for r in results if r["ok"])
    fail = sum(1 for r in results if not r["ok"])
    print(f"  Jobs: {len(results)}  OK: {ok}  Failed: {fail}  Total reviews: {total:,}")

    for r in results:
        if not r["ok"]:
            print(f"  ❌ {r['app_id']} ⭐{r['score']}star — {r.get('error', 'unknown')}")

    return results


# ── main ──────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Scrape Google Play Store reviews",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python scraper.py                                            # default app (Co-Star), 500 reviews
  python scraper.py com.costarastrology -c 100                 # 100 reviews
  python scraper.py com.spotify.music -s 5 -c 50               # 5-star only
  python scraper.py com.costarastrology -c 0                   # ALL reviews
  python scraper.py com.costarastrology -c 0 -s 1 -f json      # all 1-star, JSON only
  python scraper.py --batch apps.txt --stars 1,2,5 -c 0        # batch: all stars for all apps
        """,
    )
    parser.add_argument("app_id", nargs="?", default=DEFAULT_APP_ID,
                        help=f"Google Play app ID (default: {DEFAULT_APP_ID})")
    parser.add_argument("-c", "--count", type=int, default=DEFAULT_COUNT,
                        help="Number of reviews per app/star (0 = all; default: 500)")
    parser.add_argument("-s", "--score", type=int, choices=[1, 2, 3, 4, 5],
                        help="Filter by star rating (default: any)")
    parser.add_argument("-l", "--lang", default=DEFAULT_LANG,
                        help=f"Language code (default: {DEFAULT_LANG})")
    parser.add_argument("--country", default=DEFAULT_COUNTRY,
                        help=f"Country code (default: {DEFAULT_COUNTRY})")
    parser.add_argument("--sort", default="newest",
                        choices=["newest", "relevant", "rating"],
                        help="Sort order (default: newest)")
    parser.add_argument("-f", "--format", default="both",
                        choices=["csv", "json", "both"],
                        help="Output format (default: both)")
    parser.add_argument("-o", "--outdir", default=str(DEFAULT_OUT_DIR),
                        help="Output directory")
    parser.add_argument("--sleep", type=int, default=100,
                        help="Sleep ms between pagination requests (default: 100)")
    parser.add_argument("--max-retries", type=int, default=DEFAULT_MAX_RETRIES,
                        help=f"Max retry attempts on failure (default: {DEFAULT_MAX_RETRIES})")
    parser.add_argument("--proxy", type=str, metavar="URL",
                        help="Proxy URL, e.g. http://127.0.0.1:7890 or socks5://127.0.0.1:1080 "
                             "(or set GP_PROXY env var)")

    # Batch mode
    parser.add_argument("--batch", type=str, metavar="FILE",
                        help="File with app IDs, one per line (enables batch mode)")
    parser.add_argument("--stars", type=str,
                        help="Comma-separated star ratings for batch mode, e.g. 1,2,5")

    args = parser.parse_args()

    if args.proxy:
        stealth.set_proxy(args.proxy)
        print(f"🔀 proxy: {args.proxy}")

    if not stealth.stealth_enabled():
        print("⚠️  stealth disabled (GP_SCRAPER_NO_STEALTH=1) — requests expose default Python fingerprint")

    if args.batch:
        if args.stars:
            print(f"📋 Batch mode: {args.batch}  stars={args.stars}")
        else:
            print(f"📋 Batch mode: {args.batch}  star=any")
        run_batch(args)
        return

    # Single mode
    data = fetch_reviews(
        app_id=args.app_id,
        lang=args.lang,
        country=args.country,
        sort=SORT_MAP[args.sort],
        count=args.count,
        filter_score=args.score,
        sleep_ms=args.sleep,
        max_retries=args.max_retries,
    )

    if not data:
        print("⚠️  No reviews returned. The app may have no reviews matching your filters.")
        sys.exit(0)

    save_output(data, args.app_id, args.format, Path(args.outdir),
                score=args.score)


if __name__ == "__main__":
    main()
