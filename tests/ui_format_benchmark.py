#!/usr/bin/env python3
"""
UI 层级格式 LLM 识别对比测试

同一份数据 → 6 种输出格式 → N 个模型 → M 个问题 → 对比准确率

用法:
  python3 tests/ui_format_benchmark.py            # 跑全部
  python3 tests/ui_format_benchmark.py --list     # 列出可用模型
"""

import json, subprocess, sys, time, re, os
from collections import defaultdict

# ── 测试数据: 模拟 Android Settings 某一页 ─────────────────────────────────────

ELEMENTS = [
    {"index": 0, "text": "Wi‑Fi",             "class": "Preference", "clickable": True,  "depth": 0, "bounds": [0,0,1080,120]},
    {"index": 1, "text": "HomeWiFi",          "class": "TextView",   "clickable": False, "depth": 1, "bounds": [100,120,800,160]},
    {"index": 2, "text": "已连接",              "class": "TextView",   "clickable": False, "depth": 1, "bounds": [820,120,1050,160]},
    {"index": 3, "text": "蓝牙",                "class": "Preference", "clickable": True,  "depth": 0, "bounds": [0,170,1080,290]},
    {"index": 4, "text": "SIM 卡",             "class": "Preference", "clickable": True,  "depth": 0, "bounds": [0,300,1080,420]},
    {"index": 5, "text": "中国移动",             "class": "TextView",   "clickable": False, "depth": 1, "bounds": [100,420,700,460]},
    {"index": 6, "text": "流量用量",             "class": "Preference", "clickable": True,  "depth": 0, "bounds": [0,470,1080,590]},
    {"index": 7, "text": "热点与网络共享",       "class": "Preference", "clickable": True,  "depth": 0, "bounds": [0,600,1080,720]},
    {"index": 8, "text": "Wi‑Fi 热点",          "class": "Preference",  "clickable": True,  "depth": 1, "bounds": [100,720,1080,840]},
    {"index": 9, "text": "已关闭 / 0 台设备",     "class": "TextView",   "clickable": False, "depth": 2, "bounds": [200,840,1080,880]},
    {"index": 10,"text": "USB 网络共享",         "class": "Switch",     "clickable": True,  "depth": 1, "bounds": [100,890,1080,1010]},
    {"index": 11,"text": "VPN",                "class": "Preference", "clickable": True,  "depth": 0, "bounds": [0,1020,1080,1140]},
]

# ── 6 种输出格式 ──────────────────────────────────────────────────────────────

def indent(n):
    return "  " * n

def format_flat(elements):
    """当前 phonefast 格式: 平铺列表"""
    lines = ["Interactive elements on screen:"]
    for e in elements:
        line = f"[{e['index']}]"
        if e['text']:
            line += f' text="{e["text"]}"'
        line += f" ({e['class']})"
        if e['clickable']:
            line += " [clickable]"
        lines.append(line)
    return "\n".join(lines)


def format_markdown(elements):
    """Markdown 嵌套列表"""
    lines = ["Interactive elements on screen:"]
    for e in elements:
        prefix = indent(e['depth']) + "- "
        line = f"{prefix}[{e['index']}]"
        if e['text']:
            line += f' text="{e["text"]}"'
        line += f" ({e['class']})"
        if e['clickable']:
            line += " [clickable]"
        lines.append(line)
    return "\n".join(lines)


def format_markdown_simple(elements):
    """Markdown 嵌套列表 (简洁版)"""
    lines = []
    for e in elements:
        prefix = indent(e['depth']) + "- "
        click = " 🔘" if e['clickable'] else ""
        lines.append(f"{prefix}[{e['index']}] {e['text']} ({e['class']}){click}")
    return "\n".join(lines)


def format_path_number(elements):
    """路径编号 [1.1] 风格"""
    path_stack = []
    result = ["Interactive elements on screen:"]
    for e in elements:
        d = e['depth']
        # path_stack has d+1 elements: indices 0..d
        if len(path_stack) > d + 1:
            path_stack = path_stack[:d+1]
        if len(path_stack) == d + 1:
            path_stack[d] += 1
        else:
            path_stack.append(1)
        path = ".".join(str(n) for n in path_stack)

        line = f"{indent(d)}[{path}]"
        if e['text']:
            line += f' text="{e["text"]}"'
        line += f" ({e['class']})"
        if e['clickable']:
            line += " [clickable]"
        result.append(line)
    return "\n".join(result)


def format_breadcrumb(elements):
    """面包屑: 每行自包含 parent 路径"""
    # Build parent chain for each element
    path_texts = {}
    texts_by_index = {e['index']: e['text'] for e in elements}

    result = ["Interactive elements on screen:"]
    # Use depth to build breadcrumb
    text_stack = []
    depth_stack = []

    for e in elements:
        d = e['depth']
        # Pop until we're at the right depth
        while len(text_stack) > d:
            text_stack.pop()
            depth_stack.pop()

        if len(text_stack) == d:
            text_stack.append(e['text'])
            depth_stack.append(d)
        elif len(text_stack) > d:
            text_stack = text_stack[:d+1]
            text_stack[-1] = e['text']
        else:
            text_stack.append(e['text'])

        breadcrumb = " > ".join(text_stack)
        line = f"[{e['index']}] {breadcrumb} ({e['class']})"
        if e['clickable']:
            line += " [clickable]"
        result.append(line)
    return "\n".join(result)


def format_parent_ref(elements):
    """parent= 索引引用"""
    # Compute parent indices from depths
    parent_stack = [-1]  # stack of (index, depth)
    result = ["Interactive elements on screen:"]
    for e in elements:
        d = e['depth']
        while len(parent_stack) > 1 and parent_stack[-1][1] >= d:
            parent_stack.pop()

        parent_idx = parent_stack[-1][0] if len(parent_stack) > 1 and parent_stack[-1][1] < d else -1

        line = f"[{e['index']}] parent={parent_idx}"
        if e['text']:
            line += f' text="{e["text"]}"'
        line += f" ({e['class']})"
        if e['clickable']:
            line += " [clickable]"
        result.append(line)

        parent_stack.append((e['index'], d))
    return "\n".join(result)


def format_json_lines(elements):
    """每行一个 JSON 对象"""
    lines = []
    # Build parent mapping
    parent_stack = [(-1, -1)]  # (index, depth)
    for e in elements:
        d = e['depth']
        while len(parent_stack) > 1 and parent_stack[-1][1] >= d:
            parent_stack.pop()
        parent = parent_stack[-1][0] if parent_stack[-1][1] < d else -1
        obj = {"i": e['index'], "t": e['text'], "c": e['class'],
               "p": parent, "k": e['clickable']}
        lines.append(json.dumps(obj, ensure_ascii=False))
        parent_stack.append((e['index'], d))
    return "\n".join(lines)


FORMATS = {
    "flat":          format_flat,
    "markdown":      format_markdown,
    "markdown-s":    format_markdown_simple,
    "path-number":   format_path_number,
    "breadcrumb":    format_breadcrumb,
    "parent-ref":    format_parent_ref,
    "json-lines":    format_json_lines,
}

# ── 测试问题 ──────────────────────────────────────────────────────────────────

QUESTIONS = [
    # (编号, 问题, 简短答案关键字)
    ("Q1", "Wi-Fi 下面直接有哪些子元素？列出它们的 index",
     ["1", "2"]),  # HomeWiFi=1, 已连接=2
    ("Q2", "热点与网络共享 下面直接有哪些子元素？列出它们的 index",
     ["8", "10"]),  # Wi‑Fi 热点=8, USB 网络共享=10
    ("Q3", "Wi‑Fi 热点 的子元素是什么？",
     ["9"]),  # 已关闭=9
    ("Q4", "列出所有顶层元素（不属于其他元素的）的 index。顶层元素之间用逗号分隔",
     ["0", "3", "4", "6", "7", "11"]),  # 所有 depth=0
    ("Q5", "USB 网络共享 的父元素是什么？给出 index",
     ["7"]),  # 热点与网络共享=7
    ("Q6", "中国移动 和 蓝牙 是在同一层级吗？",
     ["否", "不是", "no"]),  # 中国移动 depth=1, 蓝牙 depth=0
    ("Q7", "index=9 的元素属于哪个父元素下面？给出父元素的 index",
     ["8"]),  # Wi‑Fi 热点
    ("Q8", "总共有几个可点击的元素？只给数字",
     ["8"]),
    ("Q9", "已连接 的父元素是什么？给出 index",
     ["0"]),  # Wi‑Fi
    ("Q10","哪些元素是 index=7（热点与网络共享）的子元素？",
     ["8", "10"]),
]

# ── Ollama 调用 ────────────────────────────────────────────────────────────────

def call_ollama(model: str, prompt: str, timeout: int = 60) -> str:
    """调用 ollama chat, 返回模型回答"""
    try:
        proc = subprocess.run(
            ["ollama", "run", model, prompt],
            capture_output=True, text=True, timeout=timeout,
            env={**os.environ, "OLLAMA_NUM_PARALLEL": "1"}
        )
        return proc.stdout.strip()
    except subprocess.TimeoutExpired:
        return "[TIMEOUT]"
    except Exception as e:
        return f"[ERROR: {e}]"


def check_answer(response: str, expected_keywords: list[str]) -> bool:
    """简单关键字匹配 (对小型模型用宽松匹配)"""
    resp_lower = response.lower()
    for kw in expected_keywords:
        if kw.lower() in resp_lower:
            return True
    return False


# ── 主流程 ────────────────────────────────────────────────────────────────────

def get_available_models() -> list[str]:
    try:
        proc = subprocess.run(["ollama", "list"], capture_output=True, text=True)
        models = []
        for line in proc.stdout.strip().split("\n")[1:]:
            parts = line.split()
            if parts:
                models.append(parts[0])
        return models
    except Exception:
        return []


def main():
    import argparse
    parser = argparse.ArgumentParser(description="UI 格式 LLM 识别测试")
    parser.add_argument("--list", action="store_true", help="列出可用模型")
    parser.add_argument("--model", "-m", help="指定模型 (默认: 全部)")
    parser.add_argument("--format", "-f", help="指定格式 (默认: 全部)")
    parser.add_argument("--question", "-q", help="指定问题 (默认: 全部)")
    parser.add_argument("--dry-run", action="store_true", help="只打印各格式输出，不调用 LLM")
    args = parser.parse_args()

    available = get_available_models()
    if args.list:
        print("可用模型:")
        for m in available:
            print(f"  {m}")
        return

    if not available:
        print("❌ 未找到 ollama 模型，请先 ollama pull <model>")
        return

    # 选模型
    if args.model:
        models = [args.model]
    else:
        models = available

    # 选格式
    if args.format:
        formats = {k: v for k, v in FORMATS.items() if args.format in k}
    else:
        formats = FORMATS

    # 选问题
    if args.question:
        qs = [q for q in QUESTIONS if args.question.upper() in q[0]]
    else:
        qs = QUESTIONS

    if args.dry_run:
        for fmt_name, fmt_fn in formats.items():
            print(f"\n{'='*60}")
            print(f"  格式: {fmt_name}")
            print(f"{'='*60}")
            print(fmt_fn(ELEMENTS))
        return

    # 跑测试
    results = defaultdict(lambda: defaultdict(list))
    total = len(models) * len(formats) * len(qs)
    done = 0

    for model in models:
        print(f"\n🚀 模型: {model}")
        for fmt_name, fmt_fn in formats.items():
            formatted = fmt_fn(ELEMENTS)

            for qid, question, keywords in qs:
                prompt = f"""下面是一个 Android 手机屏幕的 UI 元素列表。

{formatted}

请根据以上信息回答问题，只回答关键信息即可，不要解释过程。

{question}"""
                response = call_ollama(model, prompt, timeout=90)
                correct = check_answer(response, keywords)
                results[model][fmt_name].append((qid, correct))
                done += 1

                status = "✅" if correct else "❌"
                print(f"  {fmt_name:15s} {qid:3s} {status}  | 回答: {response[:80]}...")
                sys.stdout.flush()

    # ── 汇总 ──────────────────────────────────────────────────────────────────
    print("\n" + "=" * 70)
    print("汇总结果")
    print("=" * 70)

    for model in models:
        print(f"\n📊 {model}:")
        print(f"   {'格式':<15s}", end="")
        for qid, _, _ in qs:
            print(f"{qid:<6s}", end="")
        print("  准确率")
        print(f"   {'─'*15}", end="")
        for _ in qs:
            print("─────", end="")
        print("  ──────")

        for fmt_name in formats:
            fmt_results = results[model][fmt_name]
            correct_count = sum(1 for _, correct in fmt_results if correct)
            rate = correct_count / len(fmt_results) * 100 if fmt_results else 0

            print(f"   {fmt_name:<15s}", end="")
            for qid, correct in fmt_results:
                icon = "✅" if correct else "❌"
                print(f"{icon}    ", end="")
            print(f"  {rate:.0f}% ({correct_count}/{len(fmt_results)})")

    # ── 总体排名 ──────────────────────────────────────────────────────────────
    print(f"\n🏆 总体排名:")
    fmt_scores = defaultdict(int)
    for model in models:
        for fmt_name in formats:
            correct_count = sum(1 for _, correct in results[model][fmt_name] if correct)
            fmt_scores[fmt_name] += correct_count

    max_score = len(models) * len(qs)
    for fmt_name, score in sorted(fmt_scores.items(), key=lambda x: -x[1]):
        bar = "█" * int(score / max_score * 30)
        print(f"  {fmt_name:<15s} {bar} {score}/{max_score}")


if __name__ == "__main__":
    main()
