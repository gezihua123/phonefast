#!/usr/bin/env python3
"""
UI 层级格式 LLM 识别评测

设计:
  同一份 UI 数据 → 9 种输出格式 → 每个格式用 3 种不同提示词变体
  → 每轮追问 3 个核心问题 → 每个 (格式,提示词) 组合跑 3 轮
  → 跨模型参数量递增对比

用法:
  python3 tests/eval_ui_formats.py --model qwen3:0.6b           # 单模型
  python3 tests/eval_ui_formats.py --models qwen2:0.5b,qwen3:0.6b,qwen3:1.7b  # 多模型
  python3 tests/eval_ui_formats.py --rounds 5 --model qwen3:0.6b  # 自定义轮数
  python3 tests/eval_ui_formats.py --quick                      # 快速摸底 (1轮)
"""

import json, os, sys, subprocess, time, re, random
from collections import defaultdict
from pathlib import Path

# ── 配置 ──────────────────────────────────────────────────────────────────────

TESTDATA_DIR = os.path.join(os.path.dirname(__file__), "testdata")
RESULTS_DIR  = os.path.join(os.path.dirname(__file__), "results")
os.makedirs(RESULTS_DIR, exist_ok=True)

# ── 核心问题 (必须检验的核心层次理解能力) ──────────────────────────────────────

CORE_CHECKS = [
    {
        "id": "parent",
        "check": "哪个元素是 'Wi‑Fi 热点' (index=8) 的父元素？只回答 index 数字",
        "expect": ["7"],  # 热点与网络共享
        "level": "basic",
    },
    {
        "id": "children",
        "check": "'Wi‑Fi' (index=0) 下面有哪些子元素？列出它们的 index",
        "expect": ["1", "2"],  # HomeWiFi, 已连接
        "level": "basic",
    },
    {
        "id": "depth0",
        "check": "列出所有顶层元素 (depth=0, parent=-1) 的 index 数字，只给数字",
        "expect": ["0", "3", "4", "6", "7", "11"],
        "level": "hard",
    },
    {
        "id": "grandchild",
        "check": "已关闭 / 0 台设备 (index=9) 属于哪个元素的子元素？给出那个元素的 index",
        "expect": ["8"],  # Wi‑Fi 热点
        "level": "basic",
    },
    {
        "id": "hierarchy",
        "check": "'USB 网络共享' (index=10) 和 '中国移动' (index=5) 在同一层级吗？它们分别属于哪个父元素？",
        "expect": ["7", "4", "不"],  # USB parent=7, 中国移动 parent=4, 不在同一层级
        "level": "hard",
    },
]

# ── 提示词变体 ────────────────────────────────────────────────────────────────
# 同一问题用不同措辞问, 测试鲁棒性

PROMPT_VARIANTS = {
    "v1-direct": "根据以下 UI 元素列表，回答问题。\n\n{data}\n\n{question}\n\n请只给出答案，不要解释。",
    "v2-role": "你是一个 Android UI 分析工具。请分析以下界面元素并回答问题。\n\n{data}\n\n{question}",
    "v3-structured": "DATA:\n{data}\n\nTASK: {question}\n\nANSWER (简洁, 只给关键信息):",
}

# ── 工具函数 ──────────────────────────────────────────────────────────────────

def load_format(filename):
    """读取格式文件内容"""
    path = os.path.join(TESTDATA_DIR, filename)
    if not os.path.exists(path):
        return None
    return open(path, encoding="utf-8").read()


def call_ollama(model, prompt, timeout=120):
    """调用 ollama, 返回 (回答文本, 耗时秒)"""
    start = time.time()
    try:
        proc = subprocess.run(
            ["ollama", "run", model, prompt],
            capture_output=True, text=True, timeout=timeout,
        )
        elapsed = time.time() - start
        out = proc.stdout.strip()
        if not out and proc.stderr:
            out = f"[EMPTY_STDERR:{proc.stderr[:100]}]"
        return out, elapsed
    except subprocess.TimeoutExpired:
        return "[TIMEOUT]", timeout
    except Exception as e:
        return f"[ERROR:{e}]", 0


def check_answer(response, expected_keywords):
    """检查回答是否包含期望关键词 (宽松匹配)"""
    resp = response.lower()
    matched = [kw.lower() for kw in expected_keywords if kw.lower() in resp]
    return len(matched) > 0, len(matched), len(expected_keywords)


def get_model_params(model_name):
    """从模型名粗略估计参数量, 用于排序"""
    m = re.search(r'(\d+\.?\d*)b', model_name.lower())
    if m:
        return float(m.group(1))
    return 99  # unknown, sort to end


# ── 主流程 ────────────────────────────────────────────────────────────────────

def main():
    import argparse
    p = argparse.ArgumentParser()
    p.add_argument("--model", "-m", help="单个模型")
    p.add_argument("--models", help="多模型逗号分隔, 如 qwen2:0.5b,qwen3:0.6b")
    p.add_argument("--rounds", "-r", type=int, default=3, help="每组合重复轮数")
    p.add_argument("--quick", action="store_true", help="快速模式 (1轮, 无重试)")
    p.add_argument("--format", "-f", help="只测指定格式 (文件名关键字)")
    p.add_argument("--models-only", action="store_true", help="只列出需要 pull 的模型建议")
    args = p.parse_args()

    rounds = 1 if args.quick else args.rounds

    # 模型列表
    if args.model:
        models = [args.model]
    elif args.models:
        models = [m.strip() for m in args.models.split(",")]
    else:
        # 默认: 从小到大的推荐列表
        models = ["qwen2:0.5b", "qwen3:0.6b"]

    if args.models_only:
        print("建议按参数量递增测试以下模型:")
        for m in sorted(models, key=get_model_params):
            params = get_model_params(m)
            print(f"  ollama pull {m}  ({params}B)")
        return

    # 检查模型可用性
    available = set()
    try:
        result = subprocess.run(["ollama", "list"], capture_output=True, text=True)
        for line in result.stdout.strip().split("\n")[1:]:
            parts = line.split()
            if parts:
                available.add(parts[0])
    except Exception:
        pass

    # 格式文件 (排除目录和隐藏文件)
    format_files = sorted(
        [f for f in os.listdir(TESTDATA_DIR)
         if not f.startswith("_") and not f.startswith(".")
         and not os.path.isdir(os.path.join(TESTDATA_DIR, f))],
        key=lambda x: x
    )
    if args.format:
        format_files = [f for f in format_files if args.format in f]

    if not format_files:
        print("❌ 未找到格式文件, 请先运行 python3 tests/gen_ui_formats.py")
        return

    # 跑测试
    all_results = {}

    for model in models:
        if model not in available:
            print(f"\n⚠️  {model} 未安装, 正在 ollama pull...")
            try:
                subprocess.run(["ollama", "pull", model], check=True, timeout=300)
                available.add(model)
            except Exception as e:
                print(f"  ❌ 拉取失败: {e}")
                continue

        params = get_model_params(model)
        print(f"\n{'='*70}")
        print(f"🤖 {model} ({params}B) — {len(format_files)} 格式 × {len(PROMPT_VARIANTS)} 提示词 × "
              f"{len(CORE_CHECKS)} 问题 × {rounds} 轮")
        print(f"{'='*70}")

        model_results = defaultdict(lambda: {"correct": 0, "total": 0, "times": []})

        for fmt_file in format_files:
            data_content = load_format(fmt_file)
            if not data_content:
                continue

            for vname, vtemplate in PROMPT_VARIANTS.items():
                for check in CORE_CHECKS:
                    for rnd in range(rounds):
                        # 构建 prompt
                        question = check["check"]
                        prompt = vtemplate.format(data=data_content, question=question)

                        response, elapsed = call_ollama(model, prompt)

                        correct, matched, total_kw = check_answer(response, check["expect"])

                        key = f"{fmt_file}|{vname}"
                        model_results[key]["correct"] += (1 if correct else 0)
                        model_results[key]["total"] += 1
                        model_results[key]["times"].append(elapsed)

                        # 实时输出
                        icon = "✅" if correct else "❌"
                        print(f"  {fmt_file:<25s} {vname:<12s} {check['id']:<12s} "
                              f"r{rnd+1} {icon} ({matched}/{total_kw} kw) "
                              f"{elapsed:.1f}s | {response[:60].replace(chr(10),' ')}")

        all_results[model] = dict(model_results)

        # 模型小结
        print_model_summary(model, model_results, format_files)

    # ── 跨模型对比 ────────────────────────────────────────────────────────────
    if len(all_results) > 1:
        print("\n" + "=" * 70)
        print("📊 跨模型对比")
        print("=" * 70)
        print_cross_model_summary(all_results, format_files)
        save_results_json(all_results)


def print_model_summary(model, results, format_files):
    """按格式汇总"""
    print(f"\n  ── {model} 按格式汇总 ──")
    fmt_scores = defaultdict(lambda: {"correct": 0, "total": 0})

    for key, stats in results.items():
        fmt_file, _ = key.split("|")
        fmt_scores[fmt_file]["correct"] += stats["correct"]
        fmt_scores[fmt_file]["total"] += stats["total"]

    for fmt_file in format_files:
        s = fmt_scores[fmt_file]
        rate = s["correct"] / s["total"] * 100 if s["total"] > 0 else 0
        bar = "█" * int(rate / 5) + "░" * (20 - int(rate / 5))
        print(f"  {fmt_file:<30s} {bar} {rate:5.1f}% ({s['correct']}/{s['total']})")


def print_cross_model_summary(all_results, format_files):
    """跨模型对比表"""
    models = sorted(all_results.keys(), key=get_model_params)

    # 表头
    print(f"\n  {'Format':<30s}", end="")
    for m in models:
        params = get_model_params(m)
        print(f" {m.split(':')[0]:>6s}({params:.1f}B)", end="")
    print()

    print(f"  {'─'*30}", end="")
    for _ in models:
        print(f" {'─'*13}", end="")
    print()

    for fmt_file in format_files:
        print(f"  {fmt_file:<30s}", end="")
        for m in models:
            results = all_results[m]
            fmt_scores = defaultdict(lambda: {"correct": 0, "total": 0})
            for key, stats in results.items():
                f, _ = key.split("|")
                if f == fmt_file:
                    fmt_scores[f]["correct"] += stats["correct"]
                    fmt_scores[f]["total"] += stats["total"]
            s = fmt_scores[fmt_file]
            rate = s["correct"] / s["total"] * 100 if s["total"] > 0 else 0
            print(f" {rate:5.1f}% ({s['correct']:>2d}/{s['total']:>2d})", end="")
        print()

    # 按提示词变体汇总
    print(f"\n  ── 按提示词变体汇总 ──")
    variant_scores = defaultdict(lambda: {"correct": 0, "total": 0})
    for m in models:
        for key, stats in all_results[m].items():
            _, vname = key.split("|")
            variant_scores[vname]["correct"] += stats["correct"]
            variant_scores[vname]["total"] += stats["total"]

    for vname in sorted(variant_scores.keys()):
        s = variant_scores[vname]
        rate = s["correct"] / s["total"] * 100 if s["total"] > 0 else 0
        print(f"  {vname:<15s} {rate:5.1f}% ({s['correct']}/{s['total']})")


def save_results_json(all_results):
    """保存详细结果到 JSON"""
    path = os.path.join(RESULTS_DIR, f"eval_{int(time.time())}.json")
    # 简化数据结构用于存盘
    output = {}
    for model, results in all_results.items():
        output[model] = {}
        for key, stats in results.items():
            output[model][key] = {
                "correct": stats["correct"],
                "total": stats["total"],
                "rate": round(stats["correct"] / stats["total"] * 100, 1) if stats["total"] else 0,
                "avg_time": round(sum(stats["times"]) / len(stats["times"]), 1) if stats["times"] else 0,
            }

    with open(path, "w", encoding="utf-8") as f:
        json.dump(output, f, ensure_ascii=False, indent=2)
    print(f"\n  详细结果: {path}")


if __name__ == "__main__":
    main()
