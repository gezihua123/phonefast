#!/usr/bin/env python3
"""
Fix existing scraped Google Play review data.

Repairs:
  1. Corrupted reviewUrl — replaces reviewId with the correct app package name
  2. Missing userImage column — adds the column (empty values; we can't recover
     images from past runs, but the column will match the correct schema)
  3. BOM prefix on CSV header — strips U+FEFF from the first column name
  4. CRLF line endings — normalised to LF

Works on all CSV and JSON files under output/.
Safe to re-run: fixes are idempotent.

Usage:
  python fix_data.py              # fix everything under output/
  python fix_data.py --dry-run    # preview changes only
  python fix_data.py --backup     # create .bak files before modifying
"""

import argparse
import csv
import io
import json
import os
import shutil
import sys
from pathlib import Path

# Project root is one level up from this script (scripts/ -> project root)
OUTPUT_DIR = Path(__file__).resolve().parent.parent / "output"

# ── helpers ──────────────────────────────────────────────────────────

def extract_app_id(file_path: Path) -> str | None:
    """Infer app_id from directory: output/<app_id>/<star> directory/<file>."""
    try:
        return file_path.parent.parent.name
    except Exception:
        return None


def correct_review_url(record: dict, app_id: str) -> dict:
    """Fix reviewUrl so ?id=<app_id> instead of ?id=<reviewId-uuid>."""
    record["reviewUrl"] = f"https://play.google.com/store/apps/details?id={app_id}"
    return record


def ensure_user_image(record: dict) -> dict:
    """Make sure the userImage key exists."""
    if "userImage" not in record:
        record["userImage"] = ""
    return record


# ── CSV fixer ────────────────────────────────────────────────────────

def fix_csv(csv_path: Path, app_id: str, *, dry_run: bool = False,
            backup: bool = False):
    """Read, fix, and optionally write back a CSV file."""
    with open(csv_path, newline="", encoding="utf-8") as f:
        raw = f.read()

    # Strip BOM if present
    clean = raw.lstrip("﻿")

    reader = csv.DictReader(io.StringIO(clean))
    rows = list(reader)

    changes = []
    for row in rows:
        old_url = row.get("reviewUrl", "")
        corrected = correct_review_url(row, app_id)
        row = ensure_user_image(corrected)
        if old_url != row["reviewUrl"]:
            changes.append(f"  reviewUrl: {old_url[:70]}... → {row['reviewUrl']}")

    # Rebuild with correct fieldnames (includes userImage + no BOM)
    fieldnames = list(reader.fieldnames or rows[0].keys())
    if "userImage" not in fieldnames:
        fieldnames.insert(1, "userImage")  # insert after reviewId

    output = io.StringIO()
    writer = csv.DictWriter(output, fieldnames=fieldnames, lineterminator="\n")
    writer.writeheader()
    for row in rows:
        ordered = {k: row.get(k, "") for k in fieldnames}
        writer.writerow(ordered)

    if dry_run:
        print(f"[DRY RUN] {csv_path}")
        for c in changes[:3]:
            print(c)
        if len(changes) > 3:
            print(f"  ... and {len(changes) - 3} more")
        print(f"  userImage added={('userImage' not in (reader.fieldnames or []))}")
        print()
        return {"path": csv_path, "rows": len(rows), "changes": len(changes),
                "userImage_added": "userImage" not in (reader.fieldnames or [])}
    else:
        if backup:
            shutil.copy2(csv_path, str(csv_path) + ".bak")
        with open(csv_path, "w", encoding="utf-8") as f:
            f.write(output.getvalue())
        return {"path": csv_path, "rows": len(rows), "changes": len(changes),
                "userImage_added": "userImage" not in (reader.fieldnames or [])}


# ── JSON fixer ───────────────────────────────────────────────────────

def fix_json(json_path: Path, app_id: str, *, dry_run: bool = False,
             backup: bool = False):
    """Read, fix, and optionally write back a JSON file."""
    with open(json_path, encoding="utf-8") as f:
        data = json.load(f)

    changes = 0
    for record in data:
        old_url = record.get("reviewUrl", "")
        record = correct_review_url(record, app_id)
        record = ensure_user_image(record)
        if old_url != record["reviewUrl"]:
            changes += 1

    if dry_run:
        print(f"[DRY RUN] {json_path}")
        print(f"  records: {len(data)}, reviewUrl changes: {changes}")
        print(f"  userImage present: {('userImage' in data[0]) if data else 'N/A'}")
        print()
    else:
        if backup:
            shutil.copy2(json_path, str(json_path) + ".bak")
        with open(json_path, "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
            f.write("\n")

    return {"path": json_path, "records": len(data), "changes": changes}


# ── main ─────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Fix existing Google Play scraped review data",
    )
    parser.add_argument("--dry-run", action="store_true",
                        help="Preview changes without writing")
    parser.add_argument("--backup", action="store_true",
                        help="Create .bak copies before modifying")
    parser.add_argument("--output-dir", type=Path, default=OUTPUT_DIR,
                        help=f"Output directory (default: {OUTPUT_DIR})")
    args = parser.parse_args()

    if not args.output_dir.is_dir():
        print(f"❌ Output directory not found: {args.output_dir}")
        sys.exit(1)

    csv_files = sorted(args.output_dir.glob("**/*.csv"))
    json_files = sorted(args.output_dir.glob("**/*.json"))

    mode = "DRY RUN — preview only" if args.dry_run else "FIXING"
    print(f"🔧 {mode}")
    print(f"   CSV files found: {len(csv_files)}")
    print(f"   JSON files found: {len(json_files)}")
    print()

    results = []

    for fpath in csv_files:
        app_id = extract_app_id(fpath)
        if not app_id:
            print(f"⚠  Skipping {fpath} — can't determine app_id")
            continue
        result = fix_csv(fpath, app_id, dry_run=args.dry_run,
                         backup=args.backup)
        results.append(result)

    for fpath in json_files:
        app_id = extract_app_id(fpath)
        if not app_id:
            print(f"⚠  Skipping {fpath} — can't determine app_id")
            continue
        result = fix_json(fpath, app_id, dry_run=args.dry_run,
                          backup=args.backup)
        results.append(result)

    # Summary
    print("─" * 50)
    total_rows = sum(r.get("rows", r.get("records", 0)) for r in results)
    total_changes = sum(r.get("changes", 0) for r in results)
    user_image_fixes = sum(1 for r in results if r.get("userImage_added"))
    print(f"Total files processed:  {len(results)}")
    print(f"Total records:          {total_rows:,}")
    print(f"reviewUrl corrections:  {total_changes:,}")
    print(f"userImage added:        {user_image_fixes} files")
    if args.dry_run:
        print("\n💡 Re-run without --dry-run to apply changes.")
    else:
        print("\n✅ All files fixed.")


if __name__ == "__main__":
    main()
