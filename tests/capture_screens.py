#!/usr/bin/env python3
"""
从真实手机截图捕获多个 App 界面 → 生成多种格式 UI 数据文件

用法:
  python3 tests/capture_screens.py                    # 捕获 3 个界面
  python3 tests/capture_screens.py --scenes home,play # 指定场景
  python3 tests/capture_screens.py --verify           # 验证已生成数据
"""

import json, os, sys, subprocess, socket, time, base64, struct
from pathlib import Path

# ── Daemon 通信 ───────────────────────────────────────────────────────────────

SOCK_PATH = "/tmp/phonefast-501-RF8RB05GQ3L.sock"

def daemon_rpc(method, params=None, timeout=15):
    """直接向 daemon socket 发送 JSON-RPC 请求 (newline-delimited)"""
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(timeout)
    try:
        sock.connect(SOCK_PATH)
        req = json.dumps({"jsonrpc": "2.0", "method": method,
                          "params": params or {}, "id": 1})
        sock.sendall((req + "\n").encode("utf-8"))

        # Read response until newline
        buf = b""
        while True:
            chunk = sock.recv(4096)
            if not chunk:
                break
            buf += chunk
            if b"\n" in buf:
                buf = buf[:buf.index(b"\n")]
                break
        return json.loads(buf.decode("utf-8"))
    finally:
        sock.close()

def get_ui_elements():
    """获取原生 UI 元素 JSON"""
    resp = daemon_rpc("get_ui_elements", {"max_elements": 200, "summary": False})
    if "result" in resp and "elements" in resp["result"]:
        return resp["result"]["elements"]
    return []

def get_observe():
    """获取截图 + UI 元素 (base64 png)"""
    resp = daemon_rpc("observe", {"max_elements": 200, "summary": False})
    if "result" in resp:
        return resp["result"]
    return {}

# ── App 启动 ──────────────────────────────────────────────────────────────────

def phonefast_cmd(*args):
    """运行 phonefast CLI 命令"""
    subprocess.run(["phonefast", "--daemon"] + list(args),
                   capture_output=True, text=True, timeout=10)

def go_home():
    phonefast_cmd("home")
    time.sleep(1.5)

def launch_app(package):
    phonefast_cmd("launch", package)
    time.sleep(2.5)

# ── 场景定义 ──────────────────────────────────────────────────────────────────

SCENES = {
    "home": {
        "name": "桌面",
        "setup": go_home,
        "pkg": "com.sec.android.app.launcher",
    },
    "play": {
        "name": "Google Play 商店",
        "setup": lambda: launch_app("com.android.vending"),
        "pkg": "com.android.vending",
    },
    "photos": {
        "name": "相册",
        "setup": lambda: launch_app("com.google.android.apps.photos"),
        "pkg": "com.google.android.apps.photos",
    },
    "settings": {
        "name": "设置",
        "setup": lambda: launch_app("com.android.settings"),
        "pkg": "com.android.settings",
    },
}

# ── 格式转换 ──────────────────────────────────────────────────────────────────
# 接收原生 UIElement JSON → 输出各格式

def compute_parents(elements):
    """从 bounds 包含关系推断层级"""
    parents = [-1] * len(elements)

    # 已经有 parent 字段的直接用
    if any(e.get("parent") is not None for e in elements):
        return [e.get("parent", -1) for e in elements]

    # 已经有 depth 字段的推算
    has_depth = any("depth" in e for e in elements)
    if has_depth:
        depths = [e.get("depth", 0) for e in elements]
        stack = [(-1, -1)]
        for i, e in enumerate(elements):
            d = depths[i]
            while len(stack) > 1 and stack[-1][1] >= d:
                stack.pop()
            parents[i] = stack[-1][0] if stack[-1][1] < d else -1
            stack.append((i, d))
        return parents

    # Bounds 包含关系推断: 父 = 包含当前元素的最小元素
    for i in range(len(elements)):
        b = elements[i].get("bounds", [0, 0, 0, 0])
        if b[2] <= b[0] or b[3] <= b[1]:
            continue  # invalid bounds

        best_parent = -1
        best_area = float("inf")
        area_i = (b[2] - b[0]) * (b[3] - b[1])

        for j in range(len(elements)):
            if i == j:
                continue
            bj = elements[j].get("bounds", [0, 0, 0, 0])
            # 父必须完全包含子
            if bj[0] <= b[0] and bj[1] <= b[1] and bj[2] >= b[2] and bj[3] >= b[3]:
                area_j = (bj[2] - bj[0]) * (bj[3] - bj[1])
                # 严格小于 (面积相同是同层)
                if area_j < area_i and area_j < best_area:
                    best_area = area_j
                    best_parent = j

        parents[i] = best_parent

    return parents

def elem_short_class(e):
    cls = e.get("class_name", "")
    if "." in cls:
        return cls.rsplit(".", 1)[-1]
    return cls

def elem_label(e):
    """获取元素最佳显示名"""
    return e.get("text", "") or e.get("content_desc", "") or e.get("resource_id", "") or ""

# ── 格式生成器 ──────────────────────────────────────────────────────────────

def indent(n):
    return "  " * n

def generate_formats(elements, scene_name):
    """生成所有格式化输出"""
    parents = compute_parents(elements)

    # 计算 depth
    depths = [0] * len(elements)
    for i, p in enumerate(parents):
        if p == -1:
            depths[i] = 0
        else:
            depths[i] = depths[p] + 1

    for i in range(len(elements)):
        elements[i]["_depth"] = depths[i]
        elements[i]["_parent"] = parents[i]

    formats = {}

    # 01-flat
    lines = []
    for e in elements:
        lbl = elem_label(e)
        line = f'[{e["index"]}]'
        if lbl:
            line += f' text="{lbl}"'
        line += f' ({elem_short_class(e)})'
        if e.get("clickable"):
            line += " [clickable]"
        lines.append(line)
    formats["01-flat.txt"] = "\n".join(lines)

    # 02-markdown
    lines = []
    for e in elements:
        lbl = elem_label(e)
        d = depths[e["index"]]
        prefix = "  " * d + "- "
        line = f"{prefix}[{e['index']}] **{lbl}** *{elem_short_class(e)}*"
        if e.get("clickable"):
            line += " 🔘"
        lines.append(line)
    formats["02-markdown.md"] = "\n".join(lines)

    # 03-markdown-full
    lines = []
    for e in elements:
        lbl = elem_label(e)
        d = depths[e["index"]]
        prefix = "  " * d + "- "
        line = f'{prefix}[{e["index"]}] text="{lbl}" ({elem_short_class(e)})'
        if e.get("clickable"):
            line += " [clickable]"
        if e.get("resource_id"):
            rid = e["resource_id"].rsplit("/", 1)[-1] if "/" in e["resource_id"] else e["resource_id"]
            line += f' id="{rid}"'
        lines.append(line)
    formats["03-markdown-full.md"] = "\n".join(lines)

    # 04-path-number
    path_stack = []
    lines = []
    for e in elements:
        d = depths[e["index"]]
        if len(path_stack) > d + 1:
            path_stack = path_stack[:d+1]
        if len(path_stack) == d + 1:
            path_stack[d] += 1
        else:
            path_stack.append(1)
        path = ".".join(str(n) for n in path_stack)
        lbl = elem_label(e)
        line = f'{"  " * d}[{path}] text="{lbl}" ({elem_short_class(e)})'
        if e.get("clickable"):
            line += " [clickable]"
        lines.append(line)
    formats["04-path-number.txt"] = "\n".join(lines)

    # 05-breadcrumb
    text_stack = []
    lines = []
    for e in elements:
        d = depths[e["index"]]
        lbl = elem_label(e)
        if len(text_stack) > d:
            text_stack = text_stack[:d]
        if len(text_stack) == d:
            text_stack.append(lbl)
        crumb = " > ".join(text_stack)
        line = f'[{e["index"]}] {crumb} ({elem_short_class(e)})'
        if e.get("clickable"):
            line += " [clickable]"
        lines.append(line)
    formats["05-breadcrumb.txt"] = "\n".join(lines)

    # 06-parent-ref
    lines = []
    for e in elements:
        lbl = elem_label(e)
        p = e["_parent"]
        line = f'[{e["index"]}] parent={p} text="{lbl}" ({elem_short_class(e)})'
        if e.get("clickable"):
            line += " [clickable]"
        lines.append(line)
    formats["06-parent-ref.txt"] = "\n".join(lines)

    # 07-json-lines
    lines = []
    for e in elements:
        obj = {
            "i": e["index"],
            "t": elem_label(e),
            "c": elem_short_class(e),
            "p": e["_parent"],
            "dp": depths[e["index"]],
            "b": e.get("bounds", [0,0,0,0]),
            "x": e.get("center", [0,0])[0] if "center" in e else 0,
            "y": e.get("center", [0,0])[1] if "center" in e else 0,
            "clk": e.get("clickable", False),
            "en": e.get("enabled", True),
        }
        lines.append(json.dumps(obj, ensure_ascii=False))
    formats["07-json-lines.jsonl"] = "\n".join(lines)

    # 08-csv
    lines = ["index,parent,depth,text,class,clickable"]
    for e in elements:
        lbl = elem_label(e)
        lines.append(f'{e["index"]},{e["_parent"]},{depths[e["index"]]},{lbl},{elem_short_class(e)},{e.get("clickable", False)}')
    formats["08-csv.csv"] = "\n".join(lines)

    # 09-yaml
    lines = ["elements:"]
    for e in elements:
        lbl = elem_label(e)
        lines.append(f'- {{index: {e["index"]}, parent: {e["_parent"]}, depth: {depths[e["index"]]}, '
                     f'text: "{lbl}", class: {elem_short_class(e)}, clickable: {e.get("clickable", False)}}}')
    formats["09-yaml.yaml"] = "\n".join(lines)

    return formats

# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    import argparse
    p = argparse.ArgumentParser()
    p.add_argument("--scenes", default="home,play,photos", help="逗号分隔场景")
    p.add_argument("--verify", action="store_true", help="只验证")
    p.add_argument("-o", default="tests/testdata", help="输出目录")
    args = p.parse_args()

    out_dir = args.o
    os.makedirs(out_dir, exist_ok=True)

    if args.verify:
        verify_scenes(out_dir)
        return

    scenes = [s.strip() for s in args.scenes.split(",")]
    all_files = {}

    for scene_id in scenes:
        if scene_id not in SCENES:
            print(f"  ⚠️ 未知场景: {scene_id}")
            continue

        scene = SCENES[scene_id]
        print(f"\n{'='*60}")
        print(f"  📱 {scene['name']} ({scene['pkg']})")
        print(f"{'='*60}")

        # Setup
        scene["setup"]()

        # Capture UI elements
        elements = get_ui_elements()
        if not elements:
            print(f"  ❌ 未获取到元素")
            continue

        print(f"  获取到 {len(elements)} 个 UI 元素")

        # Generate formats
        formats = generate_formats(elements, scene["name"])

        # Write to scene subdirectory
        scene_dir = os.path.join(out_dir, scene_id)
        os.makedirs(scene_dir, exist_ok=True)

        for filename, content in formats.items():
            path = os.path.join(scene_dir, filename)
            with open(path, "w", encoding="utf-8") as f:
                f.write(content + "\n")
        print(f"  写入 {len(formats)} 种格式 → {scene_dir}/")

        # Also save raw JSON for reference
        raw_path = os.path.join(scene_dir, "_raw.json")
        with open(raw_path, "w", encoding="utf-8") as f:
            json.dump(elements, f, ensure_ascii=False, indent=2)
        print(f"  原始 JSON 备份: _raw.json")

        all_files[scene_id] = set(formats.keys())

    # Summary
    print(f"\n{'='*60}")
    print(f"✅ 完成! 输出目录: {out_dir}/")
    for scene_id, files in all_files.items():
        print(f"  {scene_id}/ ({len(files)} 格式)")

    print(f"\n  接下来跑 LLM 评测:")
    print(f"  python3 tests/eval_ui_formats.py --model qwen3:0.6b")


def verify_scenes(out_dir):
    """验证各场景数据一致性"""
    for scene_id in ["home", "play", "photos", "settings"]:
        scene_dir = os.path.join(out_dir, scene_id)
        raw_path = os.path.join(scene_dir, "_raw.json")
        jsonl_path = os.path.join(scene_dir, "07-json-lines.jsonl")

        if not os.path.exists(raw_path):
            continue

        raw = json.load(open(raw_path))
        jsonl_elements = []
        for line in open(jsonl_path):
            line = line.strip()
            if line.startswith("#") or not line:
                continue
            jsonl_elements.append(json.loads(line))

        ok = len(raw) == len(jsonl_elements)
        print(f"  {scene_id}: {len(raw)} elements → jsonl {len(jsonl_elements)} {'✅' if ok else '❌'}")

    print("\n✅ 验证完成")


if __name__ == "__main__":
    main()
