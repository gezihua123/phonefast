#!/usr/bin/env python3
"""
生成 UI 层级数据的多种格式文件，并验证内容一致性。

数据模拟 Android 设置 → 网络与互联网 页面，包含 3 层嵌套。

用法:
  python3 tests/gen_ui_formats.py              # 生成所有格式到 tests/testdata/
  python3 tests/gen_ui_formats.py --verify     # 验证已生成文件的一致性
  python3 tests/gen_ui_formats.py --dry-run    # 只打印不写文件
"""

import json, os, sys

# ── 元数据 ────────────────────────────────────────────────────────────────────

META = (
    "# UI 元素列表 — Android 手机屏幕 (设置 > 网络与互联网)\n"
    "# 屏幕分辨率: 1080×2400\n"
    "# 字段: index=编号 depth=层级(0=顶层) parent=父节点(-1=顶层)\n"
    "#       text=文本 class=控件类型 clickable=可点击\n"
)

# ── 测试数据: 模拟 Android Settings 页面 ──────────────────────────────────────
#     层级: 3 层 (depth 0→1→2), 6 个顶层, 12 个元素, 8 个可点击

ELEMENTS = [
    # depth=0 顶层
    {"index":0, "text":"Wi‑Fi",           "class":"Preference","clickable":True, "enabled":True, "selected":False, "depth":0,
     "resource_id":"android:id/title", "bounds":[0,0,1080,130],       "center":[540,65]},
    #   depth=1 Wi‑Fi 的子元素
    {"index":1, "text":"HomeWiFi",        "class":"TextView",  "clickable":False,"enabled":True, "selected":False, "depth":1,
     "resource_id":"android:id/summary","bounds":[100,130,800,170],  "center":[450,150]},
    {"index":2, "text":"已连接",           "class":"TextView",  "clickable":False,"enabled":True, "selected":False, "depth":1,
     "resource_id":"","bounds":[820,130,1050,170],"center":[935,150]},

    # depth=0 顶层
    {"index":3, "text":"蓝牙",             "class":"Preference","clickable":True, "enabled":True, "selected":False, "depth":0,
     "resource_id":"android:id/title", "bounds":[0,180,1080,310],     "center":[540,245]},

    # depth=0 顶层
    {"index":4, "text":"SIM 卡",          "class":"Preference","clickable":True, "enabled":True, "selected":False, "depth":0,
     "resource_id":"android:id/title", "bounds":[0,320,1080,450],     "center":[540,385]},
    #   depth=1 SIM 卡的子元素
    {"index":5, "text":"中国移动",          "class":"TextView",  "clickable":False,"enabled":True, "selected":False, "depth":1,
     "resource_id":"android:id/summary","bounds":[100,450,700,490],  "center":[400,470]},

    # depth=0 顶层
    {"index":6, "text":"流量用量",          "class":"Preference","clickable":True, "enabled":True, "selected":False, "depth":0,
     "resource_id":"android:id/title", "bounds":[0,500,1080,630],     "center":[540,565]},

    # depth=0 顶层
    {"index":7, "text":"热点与网络共享",    "class":"Preference","clickable":True, "enabled":True, "selected":False, "depth":0,
     "resource_id":"android:id/title", "bounds":[0,640,1080,770],     "center":[540,705]},
    #   depth=1 热点与网络共享的子元素
    {"index":8, "text":"Wi‑Fi 热点",       "class":"Preference","clickable":True, "enabled":True, "selected":False, "depth":1,
     "resource_id":"android:id/title", "bounds":[100,770,1080,900],   "center":[590,835]},
    #     depth=2 Wi‑Fi 热点的子元素
    {"index":9, "text":"已关闭 / 0 台设备",  "class":"TextView",  "clickable":False,"enabled":True, "selected":False, "depth":2,
     "resource_id":"android:id/summary","bounds":[200,900,1080,940], "center":[640,920]},
    #   depth=1 热点与网络共享的另一个子元素
    {"index":10,"text":"USB 网络共享",      "class":"Switch",    "clickable":True, "enabled":True, "selected":False, "depth":1,
     "resource_id":"android:id/switch_widget","bounds":[100,950,1080,1080],"center":[590,1015]},

    # depth=0 顶层
    {"index":11,"text":"VPN",              "class":"Preference","clickable":True, "enabled":False,"selected":False, "depth":0,
     "resource_id":"android:id/title", "bounds":[0,1090,1080,1220],   "center":[540,1155]},
]

# ── 计算 parent ───────────────────────────────────────────────────────────────

def compute_parents(elements):
    stack = [(-1, -1)]
    parents = []
    for e in elements:
        d = e["depth"]
        while len(stack) > 1 and stack[-1][1] >= d:
            stack.pop()
        p = stack[-1][0] if stack[-1][1] < d else -1
        parents.append(p)
        stack.append((e["index"], d))
    return parents

PARENTS = compute_parents(ELEMENTS)
for e, p in zip(ELEMENTS, PARENTS):
    e["parent"] = p

# ── 辅助 ──────────────────────────────────────────────────────────────────────

def indent(n): return "  " * n

def label(e):
    """最佳显示文本: text > content_desc > resource_id 最后一段"""
    t = e.get("text", "") or e.get("content_desc", "")
    if t:
        return t
    rid = e.get("resource_id", "")
    if rid and "/" in rid:
        return rid.rsplit("/", 1)[-1]
    return ""

def short_class(e):
    c = e["class"]
    return c.rsplit(".",1)[-1] if "." in c else c

# ── 9 种输出格式 ─────────────────────────────────────────────────────────────

def fmt_flat(elements):
    """01-flat: 当前 phonefast 平铺格式，无层级"""
    lines = [META, "Interactive elements on screen:"]
    for e in elements:
        line = f'[{e["index"]}] text="{label(e)}" ({short_class(e)})'
        if e["clickable"]: line += " [clickable]"
        if not e["enabled"]: line += " [disabled]"
        lines.append(line)
    return "\n".join(lines)

def fmt_markdown(elements):
    """02-markdown: Markdown 嵌套列表，缩进表达层级"""
    lines = [META, "## Interactive elements on screen:\n"]
    for e in elements:
        d = e["depth"]
        line = f'{indent(d)}- [{e["index"]}] **{label(e)}** *{short_class(e)}*'
        if e["clickable"]: line += " 🔘"
        if not e["enabled"]: line += " 🚫"
        lines.append(line)
    return "\n".join(lines)

def fmt_markdown_full(elements):
    """03-markdown-full: Markdown 嵌套列表 + 完整属性"""
    lines = [META, "## Interactive elements on screen:\n"]
    for e in elements:
        d = e["depth"]
        line = f'{indent(d)}- [{e["index"]}] text="{label(e)}" ({short_class(e)})'
        if e["clickable"]: line += " [clickable]"
        if not e["enabled"]: line += " [disabled]"
        lines.append(line)
    return "\n".join(lines)

def fmt_path_number(elements):
    """04-path-number: [1.1] 路径编号 + 缩进"""
    lines = [META, "Interactive elements on screen:"]
    path_stack = []
    for e in elements:
        d = e["depth"]
        if len(path_stack) > d + 1:  path_stack = path_stack[:d+1]
        if len(path_stack) == d + 1: path_stack[d] += 1
        else:                        path_stack.append(1)
        path = ".".join(str(n) for n in path_stack)
        line = f'{indent(d)}[{path}] text="{label(e)}" ({short_class(e)})'
        if e["clickable"]: line += " [clickable]"
        if not e["enabled"]: line += " [disabled]"
        lines.append(line)
    return "\n".join(lines)

def fmt_breadcrumb(elements):
    """05-breadcrumb: 面包屑路径，每行自包含"""
    lines = [META, "Interactive elements on screen:"]
    text_stack = []
    for e in elements:
        d = e["depth"]
        if len(text_stack) > d: text_stack = text_stack[:d]
        if len(text_stack) > d: text_stack[d] = label(e)
        else:                   text_stack.append(label(e))
        crumb = " > ".join(text_stack)
        line = f'[{e["index"]}] {crumb} ({short_class(e)})'
        if e["clickable"]: line += " [clickable]"
        if not e["enabled"]: line += " [disabled]"
        lines.append(line)
    return "\n".join(lines)

def fmt_parent_ref(elements):
    """06-parent-ref: parent= 显式引用"""
    lines = [META, "Interactive elements on screen:"]
    for e in elements:
        p = e["parent"]
        line = f'[{e["index"]}] parent={p} depth={e["depth"]} text="{label(e)}" ({short_class(e)})'
        if e["clickable"]: line += " [clickable]"
        if not e["enabled"]: line += " [disabled]"
        lines.append(line)
    return "\n".join(lines)

def fmt_json_lines(elements):
    """07-json-lines: 每行一个 JSON，字段见 meta 说明"""
    lines = [META]
    for e in elements:
        obj = {
            "i": e["index"],     # index
            "t": label(e),       # text
            "c": short_class(e), # class
            "p": e["parent"],    # parent index (-1 = root)
            "dp": e["depth"],    # depth
            "b": e["bounds"],    # [left, top, right, bottom]
            "x": e["center"][0], # centerX (tap坐标)
            "y": e["center"][1], # centerY
            "clk": e["clickable"],
            "en": e["enabled"],
        }
        lines.append(json.dumps(obj, ensure_ascii=False))
    return "\n".join(lines)

def fmt_csv(elements):
    """08-csv: CSV 表格"""
    lines = [META, "index,parent,depth,text,class,clickable,enabled"]
    for e in elements:
        lines.append(f'{e["index"]},{e["parent"]},{e["depth"]},{label(e)},{short_class(e)},{e["clickable"]},{e["enabled"]}')
    return "\n".join(lines)

def fmt_yaml(elements):
    """09-yaml: YAML 风格"""
    lines = [META, "elements:"]
    for e in elements:
        lines.append(
            f'- {{index: {e["index"]}, parent: {e["parent"]}, depth: {e["depth"]}, '
            f'text: "{label(e)}", class: {short_class(e)}, '
            f'clickable: {str(e["clickable"]).lower()}, enabled: {str(e["enabled"]).lower()}}}')
    return "\n".join(lines)

def fmt_xml(elements):
    """10-xml: uiautomator XML 嵌套格式"""
    lines = ['<?xml version="1.0" encoding="UTF-8"?>',
             f'<!-- {META.strip().replace(chr(10), " ")} -->',
             '<hierarchy rotation="0" width="1080" height="2400">']

    indent_xml = "  "
    # Group children by parent index
    children_of = {i: [] for i in range(-1, len(elements))}
    for e in elements:
        children_of[e["parent"]].append(e)

    def write_node(e, depth):
        attrs = f' index="{e["index"]}" text="{label(e)}" class="{short_class(e)}"'
        if e["clickable"]:
            attrs += ' clickable="true"'
        if not e["enabled"]:
            attrs += ' enabled="false"'
        attrs += f' bounds="[{e["bounds"][0]},{e["bounds"][1]}][{e["bounds"][2]},{e["bounds"][3]}]"'

        kids = children_of.get(e["index"], [])
        if kids:
            lines.append(f'{indent_xml * depth}<node{attrs}>')
            for child in kids:
                write_node(child, depth + 1)
            lines.append(f'{indent_xml * depth}</node>')
        else:
            lines.append(f'{indent_xml * depth}<node{attrs} />')

    for root in elements:
        if root["parent"] == -1:
            write_node(root, 0)

    lines.append('</hierarchy>')
    return "\n".join(lines)

FORMATS = {
    "01-flat.txt":           fmt_flat,
    "02-markdown.md":        fmt_markdown,
    "03-markdown-full.md":   fmt_markdown_full,
    "04-path-number.txt":    fmt_path_number,
    "05-breadcrumb.txt":     fmt_breadcrumb,
    "06-parent-ref.txt":     fmt_parent_ref,
    "07-json-lines.jsonl":   fmt_json_lines,
    "08-csv.csv":            fmt_csv,
    "09-yaml.yaml":          fmt_yaml,
    "10-xml.xml":            fmt_xml,
}

# ── 校验基准 ──────────────────────────────────────────────────────────────────

EXPECTED = {
    "count": 12,
    "indices": [0,1,2,3,4,5,6,7,8,9,10,11],
    "texts": [label(e) for e in ELEMENTS],
    "clickable_count": 8,       # 0,3,4,6,7,8,10,11
    "parents": PARENTS,         # [-1,0,0,-1,-1,4,-1,-1,7,8,7,-1]
    "max_depth": 2,
    "top_level": [0,3,4,6,7,11],
    "depth_counts": {0:6, 1:5, 2:1},
}

# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    import argparse
    p = argparse.ArgumentParser()
    p.add_argument("--verify", action="store_true")
    p.add_argument("--dry-run", action="store_true")
    p.add_argument("-o", default="tests/testdata")
    args = p.parse_args()

    out_dir = args.o
    os.makedirs(out_dir, exist_ok=True)

    if args.verify:
        verify_all(out_dir)
        return

    if args.dry_run:
        for name, fn in FORMATS.items():
            print(f"\n{'='*60}\n  {name}\n{'='*60}")
            print(fn(ELEMENTS))
        return

    for filename, fn in FORMATS.items():
        path = os.path.join(out_dir, filename)
        content = fn(ELEMENTS)
        with open(path, "w", encoding="utf-8") as f:
            f.write(content + "\n")
        size = os.path.getsize(path)
        print(f"  ✅ {filename:<30s} {size:>6d} bytes")

    verify_path = os.path.join(out_dir, "_expected.json")
    with open(verify_path, "w", encoding="utf-8") as f:
        json.dump(EXPECTED, f, ensure_ascii=False, indent=2)
    print(f"  ✅ _expected.json")

    print(f"\n  共 {len(FORMATS)} 种格式 → {out_dir}/")
    print(f"  python3 tests/eval_ui_formats.py --model qwen3:0.6b")


def verify_all(out_dir):
    errors = []
    for filename in FORMATS:
        path = os.path.join(out_dir, filename)
        if not os.path.exists(path):
            errors.append(f"  缺少: {filename}")
            continue
        content = open(path, encoding="utf-8").read()
        for e in ELEMENTS:
            t = label(e)
            if t and t not in content:
                errors.append(f"  {filename}: 缺少文本 '{t}'")

    jsonl_path = os.path.join(out_dir, "07-json-lines.jsonl")
    if os.path.exists(jsonl_path):
        ref = []
        for line in open(jsonl_path, encoding="utf-8"):
            line = line.strip()
            if line.startswith("#") or not line: continue
            ref.append(json.loads(line))
        for i, (r, exp) in enumerate(zip(ref, PARENTS)):
            if r["p"] != exp:
                errors.append(f"  json-lines[{i}]: parent={r['p']} expected={exp}")

    if errors:
        print("❌ 验证失败:"); [print(e) for e in errors]; sys.exit(1)
    else:
        print("✅ 格式一致，数据正确")
        print(f"   元素: {EXPECTED['count']}  可点击: {EXPECTED['clickable_count']}"
              f"  最大深度: {EXPECTED['max_depth']}  顶层: {EXPECTED['top_level']}")


if __name__ == "__main__":
    main()
