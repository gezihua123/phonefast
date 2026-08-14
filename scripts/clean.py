#!/usr/bin/env python3
"""
清理 output/ 中的无用文件。

删除:
  - *.csv        — JSON 的冗余副本
  - *.csv.bak    — fix_data.py 的备份
  - *.json.bak   — fix_data.py 的备份
  - all/         — 未按星级过滤的混合抓取目录
  - .DS_Store    — macOS 元数据
  - 无关 App     — com.floatpictxt.overlay, appstore/, com.daily.chineseastro
  - 空目录

保留:
  output/<app_id>/<N>star/<app_id>_<N>star_*.json

Usage:
  python3 scripts/clean.py            # 执行清理
  python3 scripts/clean.py --dry-run  # 预览，不删除
"""

from __future__ import annotations

import argparse
import shutil
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parent.parent
OUTPUT_DIR = PROJECT_ROOT / "output"

# Apps to remove entirely (unrelated or insufficient data)
REMOVE_APPS = [
    "com.floatpictxt.overlay",   # floating picture overlay, not in analysis scope
    "com.daily.chineseastro",    # only 6 reviews, not in analysis
]

# Directories to remove entirely
REMOVE_DIRS = [
    "appstore",                  # iOS App Store data, not Google Play
]


def fmt_size(path: Path) -> str:
    """Human-readable size of a file or directory."""
    total = 0
    if path.is_file():
        total = path.stat().st_size
    elif path.is_dir():
        total = sum(
            f.stat().st_size for f in path.rglob("*") if f.is_file()
        )
    if total < 1024:
        return f"{total}B"
    elif total < 1024 * 1024:
        return f"{total / 1024:.0f}KB"
    else:
        return f"{total / (1024 * 1024):.1f}MB"


def fmt_count(n: int) -> str:
    """Plural-aware count string."""
    return f"{n} 个" if n > 0 else "无"


class Cleaner:
    def __init__(self, dry_run: bool = False) -> None:
        self.dry_run = dry_run
        self.total_size = 0
        self.total_files = 0
        self.total_dirs = 0
        self.output_dir = OUTPUT_DIR

    def _rm(self, path: Path, desc: str) -> None:
        """Remove a file or directory, with logging."""
        size = fmt_size(path)
        is_dir = path.is_dir()
        self.total_size += sum(
            f.stat().st_size for f in path.rglob("*") if f.is_file()
        ) if is_dir else (path.stat().st_size if path.is_file() else 0)
        self.total_files += (1 if not is_dir else 0)
        self.total_dirs += (1 if is_dir else 0)

        icon = "📁" if is_dir else "📄"
        print(f"  {icon} {desc}")
        print(f"     {path}")
        print(f"     {size}")

        if self.dry_run:
            print(f"     (dry-run — 跳过)")
        else:
            if is_dir:
                shutil.rmtree(path)
            else:
                path.unlink()
            print(f"     ✅ 已删除")

    def run(self) -> None:
        size_before = fmt_size(self.output_dir)
        print("=" * 50)
        print("🧹 output/ 清理")
        print("=" * 50)
        print(f"目录 : {self.output_dir}")
        print(f"模式 : {'dry-run (预览, 不删除)' if self.dry_run else 'LIVE (执行删除)'}")
        print(f"清理前: {size_before}")
        print()

        # ── 1. CSV / .bak 文件 ──
        print("── 1. CSV 和 .bak 备份文件 ──")
        patterns = ["*.csv", "*.csv.bak", "*.json.bak"]
        removed = 0
        for pattern in patterns:
            for f in self.output_dir.rglob(pattern):
                self._rm(f, f"冗余文件: {f.name}")
                removed += 1
        if removed == 0:
            print("  ⏭  未发现")
        print()

        # ── 2. all/ 目录 ──
        print("── 2. all/ 混合抓取目录 ──")
        all_dirs = list(self.output_dir.rglob("all"))
        if all_dirs:
            for d in all_dirs:
                parent = d.parent.name
                self._rm(d, f"未过滤抓取: {parent}/all/")
        else:
            print("  ⏭  未发现")
        print()

        # ── 3. 无关 App ──
        print("── 3. 无关 App ──")
        for app_id in REMOVE_APPS:
            path = self.output_dir / app_id
            if path.exists():
                self._rm(path, f"无关 App: {app_id}")
            else:
                print(f"  ⏭  {app_id} — 不存在, 跳过")
        for dir_name in REMOVE_DIRS:
            path = self.output_dir / dir_name
            if path.exists():
                self._rm(path, f"无关目录: {dir_name}")
            else:
                print(f"  ⏭  {dir_name} — 不存在, 跳过")
        print()

        # ── 4. .DS_Store ──
        print("── 4. .DS_Store 文件 ──")
        ds_files = list(self.output_dir.rglob(".DS_Store"))
        if ds_files:
            for f in ds_files:
                self._rm(f, "macOS 元数据")
        else:
            print("  ⏭  未发现")
        print()

        # ── 5. 空目录 ──
        print("── 5. 空目录 ──")
        empty_dirs = sorted(
            [d for d in self.output_dir.rglob("*") if d.is_dir() and not any(d.iterdir())],
            reverse=True,  # deepest first
        )
        if empty_dirs:
            for d in empty_dirs:
                self._rm(d, f"空目录: {d.relative_to(self.output_dir)}")
        else:
            print("  ⏭  未发现")
        print()

        # ── summary ──
        size_after = fmt_size(self.output_dir)
        print("=" * 50)
        print("📊 清理汇总")
        print("=" * 50)
        print(f"  清理前 : {size_before}")
        print(f"  清理后 : {size_after}")
        print(f"  删除文件 : {fmt_count(self.total_files)}")
        print(f"  删除目录 : {fmt_count(self.total_dirs)}")
        print()

        # show preserved structure
        remaining_apps = sorted(
            d.name for d in self.output_dir.iterdir()
            if d.is_dir() and d.name not in REMOVE_APPS + REMOVE_DIRS
        )
        print("保留结构:")
        print(f"  output/ ({len(remaining_apps)} apps × 1★/2★/5★ JSON)")
        for app in remaining_apps:
            stars = sorted(
                d.name for d in (self.output_dir / app).iterdir()
                if d.is_dir() and "star" in d.name
            )
            print(f"    {app}/  ({', '.join(stars)})")

        if self.dry_run:
            print(f"\n🔍 预览完成。去掉 --dry-run 执行实际删除。")
        else:
            print(f"\n✅ 清理完成。")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="清理 output/ 中的无用文件（CSV / .bak / all/ / .DS_Store / 无关 App）",
    )
    parser.add_argument(
        "--dry-run", action="store_true",
        help="预览模式：列出将删除的文件，但不执行删除",
    )
    parser.add_argument(
        "--output", type=str, default=None,
        help=f"指定 output 目录（默认: {OUTPUT_DIR}）",
    )
    args = parser.parse_args()

    cleaner = Cleaner(dry_run=args.dry_run)
    if args.output:
        cleaner.output_dir = Path(args.output).resolve()
    cleaner.run()


if __name__ == "__main__":
    main()
