#!/usr/bin/env python3
"""
phonefast 准出测试 (Gate Test)
发布/PR 合并前运行，覆盖基础功能、MCP 协议、性能基准。

用法:
  python3 tests/gate.py              # 全部测试
  python3 tests/gate.py --quick      # 快速模式（跳过性能基准）
  python3 tests/gate.py --perf       # 仅性能基准
"""

import os, sys, time, subprocess, shutil, argparse, json

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_DIR = os.path.dirname(SCRIPT_DIR)
BINARY = os.path.join(PROJECT_DIR, "dist", "phonefast")
JAR = os.path.join(PROJECT_DIR, "dist", "scrcpy-server.jar")
VERSION_FILE = os.path.join(PROJECT_DIR, "dist", "scrcpy-server.version")

class Gate:
    def __init__(self):
        self.pass_count = 0
        self.fail_count = 0
        self.skip_count = 0
        self.start_time = time.time()

    def elapsed(self):
        return f"{time.time() - self.start_time:.0f}s"

    def header(self, title):
        print(f"\n{'─'*55}")
        print(f"  [{self.elapsed()}] {title}")
        print(f"{'─'*55}")

    def ok(self, msg):
        self.pass_count += 1
        print(f"  ✓  {msg}")

    def fail(self, msg, reason=""):
        self.fail_count += 1
        detail = f" — {reason}" if reason else ""
        print(f"  ✗  {msg}{detail}")

    def skip(self, msg, reason=""):
        self.skip_count += 1
        detail = f" ({reason})" if reason else ""
        print(f"  ⊘  {msg}{detail}")

    def run_cmd(self, args, timeout=30, cwd=None, check=False, capture=True):
        """Run a command, return (returncode, stdout, stderr)."""
        try:
            r = subprocess.run(
                args, capture_output=capture, text=True,
                timeout=timeout, cwd=cwd or PROJECT_DIR
            )
            return r.returncode, r.stdout, r.stderr
        except subprocess.TimeoutExpired:
            return -1, "", f"timeout after {timeout}s"
        except Exception as e:
            return -1, "", str(e)

    def check_prerequisites(self):
        self.header("0. Prerequisites")

        if os.path.isfile(BINARY):
            size = os.path.getsize(BINARY)
            self.ok(f"binary exists ({size//1024//1024}MB)")
        else:
            self.fail("binary not found", BINARY)
            sys.exit(1)

        if os.path.isfile(JAR):
            size = os.path.getsize(JAR)
            self.ok(f"scrcpy jar ({size//1024}KB)")
        else:
            self.fail("scrcpy jar not found", JAR)

        if os.path.isfile(VERSION_FILE):
            ver = open(VERSION_FILE).read().strip()
            self.ok(f"version file ({ver})")
        else:
            self.fail("version file not found", VERSION_FILE)

        if shutil.which("ffmpeg"):
            rc, out, _ = self.run_cmd(["ffmpeg", "-version"], timeout=5)
            # Extract version: "ffmpeg version 8.0.1 Copyright ..." → "8.0.1"
            ver_line = out.split("\n")[0] if out else ""
            parts = ver_line.split()
            ff_ver = parts[2] if len(parts) > 2 else "?"
            self.ok(f"ffmpeg ({ff_ver})")
        else:
            self.fail("ffmpeg not in PATH")

        if shutil.which("adb"):
            rc, out, _ = self.run_cmd(["adb", "devices"], timeout=5)
            device_lines = [l for l in out.split("\n") if "\tdevice" in l]
            if device_lines:
                self.ok(f"device connected ({len(device_lines)} device(s))")
            else:
                self.fail("no device connected")
                sys.exit(1)
        else:
            self.fail("adb not in PATH")
            sys.exit(1)

    def go_unit_tests(self):
        self.header("1. Go Unit Tests")
        rc, out, err = self.run_cmd(["go", "test", "./..."], timeout=120)
        if rc == 0:
            self.ok("go test ./...")
        else:
            # Show last 10 lines
            lines = (out + err).strip().split("\n")
            for line in lines[-10:]:
                print(f"      {line}")
            self.fail("go test ./...", "unit tests failed")

    def go_build(self):
        self.header("2. Build")
        rc, out, err = self.run_cmd(
            ["go", "build", "-o", BINARY, "./cmd/phonefast/"], timeout=60
        )
        if rc == 0:
            self.ok("go build")
            # Sync jar + version to dist/ (skip if already same dir)
            dst = os.path.dirname(BINARY)
            jar_dst = os.path.join(dst, os.path.basename(JAR))
            ver_dst = os.path.join(dst, "scrcpy-server.version")
            if os.path.isfile(JAR) and os.path.abspath(JAR) != os.path.abspath(jar_dst):
                shutil.copy2(JAR, dst)
            if os.path.isfile(VERSION_FILE):
                ver = open(VERSION_FILE).read().strip()
                with open(ver_dst, "w") as f:
                    f.write(ver)
        else:
            self.fail("go build", err[:200])

    def smoke_test(self):
        self.header("3. Release Smoke Test (test_release.py)")
        rc, out, err = self.run_cmd(
            ["python3", os.path.join(SCRIPT_DIR, "test_release.py")], timeout=120
        )
        print(out, end="")
        if rc == 0:
            p = out.count("✓")
            self.ok(f"test_release.py ({p} checks passed)")
        else:
            f = out.count("✗")
            self.fail("test_release.py", f"{f} failures")

    def mcp_protocol_test(self):
        self.header("4. MCP Protocol Test (test_mcp.py)")
        rc, out, err = self.run_cmd(
            ["python3", os.path.join(SCRIPT_DIR, "test_mcp.py")], timeout=120
        )
        # Show summary lines
        for line in out.strip().split("\n"):
            if any(kw in line for kw in ("PASS", "FAIL", "Failed", "Results")):
                print(f"    {line.strip()}")
        if rc == 0:
            p = out.count("✓")
            self.ok(f"test_mcp.py ({p} checks passed)")
        else:
            self.fail("test_mcp.py", "protocol test failed")

    def daemon_health(self):
        self.header("5. Daemon Health")

        # Stop any existing daemon
        self.run_cmd([BINARY, "daemon", "--stop"], timeout=10)
        time.sleep(1)

        # Start daemon
        rc, out, _ = self.run_cmd([BINARY, "daemon"], timeout=10)
        time.sleep(3)

        # Status
        rc, out, _ = self.run_cmd([BINARY, "daemon", "--status"], timeout=5)
        if "running" in out:
            self.ok("daemon running")
        else:
            self.fail("daemon not running", out[:100])

        # Control test
        rc, out, _ = self.run_cmd([BINARY, "--daemon", "back"], timeout=10)
        if rc == 0 and "Back" in out:
            self.ok("daemon back")
        else:
            self.fail("daemon back", out[:100])

        # Screenshot test
        tmp = "/tmp/gate_daemon_screen.png"
        rc, out, _ = self.run_cmd(
            [BINARY, "--daemon", "screenshot", tmp], timeout=30
        )
        if rc == 0 and os.path.isfile(tmp):
            size = os.path.getsize(tmp)
            if size > 1000:
                self.ok(f"daemon screenshot ({size//1024}KB)")
            else:
                self.fail("daemon screenshot", f"file too small: {size}")
        else:
            self.fail("daemon screenshot", out[:100])

    def benchmark(self):
        self.header("6. Performance Benchmark (benchmark.py --quick)")
        rc, out, err = self.run_cmd(
            ["python3", os.path.join(SCRIPT_DIR, "benchmark.py"), "--quick"],
            timeout=120
        )
        print(out, end="")
        if rc == 0:
            self.ok("benchmark.py --quick")
        else:
            self.fail("benchmark.py --quick", "benchmark failed")

    def latency_threshold(self):
        self.header("7. Daemon Latency Threshold")

        ok = True
        for i in range(1, 4):
            t0 = time.perf_counter()
            rc, _, _ = self.run_cmd([BINARY, "--daemon", "back"], timeout=10)
            t1 = time.perf_counter()
            lat_ms = int((t1 - t0) * 1000)
            status = "✓" if lat_ms <= 100 else "✗"
            print(f"    {status}  back #{i}: {lat_ms}ms")
            if lat_ms > 100:
                ok = False

        if ok:
            self.ok("daemon latency < 100ms")
        else:
            self.fail("daemon latency", "exceeded 100ms threshold")

    def summary(self):
        self.header("Summary")
        total = int(time.time() - self.start_time)
        print(f"\n  Total time: {total}s")
        print(f"  Pass:       {self.pass_count}")
        print(f"  Fail:       {self.fail_count}")
        print(f"  Skip:       {self.skip_count}\n")

        if self.fail_count > 0:
            print(f"GATE FAILED — {self.fail_count} check(s) failed")
            sys.exit(1)
        else:
            print(f"GATE PASSED — all {self.pass_count} checks passed")
            sys.exit(0)


def main():
    parser = argparse.ArgumentParser(description="phonefast Gate Test")
    parser.add_argument("--quick", action="store_true", help="Skip performance benchmark")
    parser.add_argument("--perf", action="store_true", help="Only performance benchmark")
    args = parser.parse_args()

    gate = Gate()

    if args.perf:
        # Perf-only: build, start daemon, run benchmark + latency
        gate.check_prerequisites()
        gate.go_build()
        gate.daemon_health()
        gate.benchmark()
        gate.latency_threshold()
        gate.summary()
        return

    gate.check_prerequisites()
    gate.go_unit_tests()
    gate.go_build()
    gate.smoke_test()
    gate.mcp_protocol_test()
    gate.daemon_health()

    if not args.quick:
        gate.benchmark()
        gate.latency_threshold()
    else:
        gate.skip("Performance Benchmark", "--quick mode")
        gate.skip("Latency Threshold", "--quick mode")

    gate.summary()


if __name__ == "__main__":
    main()
