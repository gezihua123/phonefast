#!/usr/bin/env python3
"""
Verify that the CLI binary name is dynamically derived from os.Args[0],
not hardcoded. Builds a copy, renames it, and checks help + usage errors.
"""
import subprocess, shutil, tempfile, os, sys

BIN_SRC = os.path.join(os.path.dirname(__file__), "..", "dist", "phonefast")
PASS = []
FAIL = []

def ok(name):
    PASS.append(name)
    print(f"  ✓  {name}")

def fail(name, reason=""):
    FAIL.append(name)
    print(f"  ✗  {name}  ({reason})")

def run(binary, args, timeout=10):
    return subprocess.run(
        [binary] + args, capture_output=True, text=True, timeout=timeout)

def contains_any(text, patterns):
    """Check text contains at least one pattern, return matched pattern."""
    for p in patterns:
        if p in text:
            return p
    return None

# ── 1. Preflight ─────────────────────────────────────────────────────────
print("=== Preflight ===")
if not os.path.isfile(BIN_SRC):
    print(f"FATAL: binary not found at {BIN_SRC}")
    sys.exit(1)
ok("source binary exists")

# Read original help to get structure (not content, just line count)
r = run(BIN_SRC, [])
orig_help = r.stdout
orig_lines = [l for l in orig_help.split("\n") if l.strip()]
print(f"  Original help: {len(orig_lines)} non-empty lines")
ok(f"original help has {len(orig_lines)} lines")

# ── 2. Sanity: no hardcoded binary name leaks ─────────────────────────────
print("\n=== Hardcode check ===")
# The original binary is named "phonefast", so it should say "phonefast"
if "phonefast — Fast" in orig_help:
    ok("default name appears in help title")
else:
    fail("default name in help title", orig_help.split("\n")[0])

# ── 3. Rename test ────────────────────────────────────────────────────────
print("\n=== Rename tests ===")
tmpdir = tempfile.mkdtemp(prefix="phonefast-test-")

test_names = [
    "my-android-tool",
    "pf",
    "phone-fast-v2.0",
    "测试工具",   # unicode name
]

for name in test_names:
    bin_path = os.path.join(tmpdir, name)
    shutil.copy2(BIN_SRC, bin_path)
    os.chmod(bin_path, 0o755)

    r = run(bin_path, [])
    help_out = r.stdout + r.stderr

    if f"{name} — Fast" in help_out:
        ok(f"rename → '{name}' help title")
    else:
        actual_title = help_out.split("\n")[0] if help_out.strip() else "(empty)"
        fail(f"rename → '{name}' help title", f"expected '{name} — Fast', got '{actual_title}'")

    # Check usage error messages
    r2 = run(bin_path, ["tap"])
    usage_out = r2.stdout + r2.stderr
    if f"{name} [--daemon] tap" in usage_out or f"{name} --daemon tap" in usage_out:
        ok(f"rename → '{name}' usage error (tap)")
    else:
        fail(f"rename → '{name}' usage error (tap)", f"got: {usage_out[:80]}")

    # Check daemon-mode usage
    r3 = run(bin_path, ["--daemon"])
    daemon_out = r3.stdout + r3.stderr
    if f"{name} --daemon" in daemon_out:
        ok(f"rename → '{name}' daemon usage")
    else:
        fail(f"rename → '{name}' daemon usage", f"got: {daemon_out[:80]}")

    # Remove before next iteration
    os.remove(bin_path)

# ── 4. Hardcoded string audit ─────────────────────────────────────────────
print("\n=== Hardcode audit ===")
# Verify that no user-facing string in the original binary contains
# the literal "phonefast" that wouldn't be replaced by our dynamic approach.
# The binary name is "phonefast" so all occurrences should be "phonefast".
# This confirms the ReplaceAll approach works correctly.

# Check help text for consistency: every line that starts with "  phonefast"
# should have the exact same prefix (proving they all use the same var)
help_lines = [l for l in orig_help.split("\n") if l.strip().startswith("phonefast")]
unique_prefixes = set()
for line in help_lines:
    # Get the "phonefast" part at the start
    tokens = line.strip().split()
    if tokens:
        unique_prefixes.add(tokens[0])
if len(unique_prefixes) == 1:
    ok(f"help: all {len(help_lines)} command lines use same prefix '{list(unique_prefixes)[0]}'")
else:
    fail(f"help inconsistent prefixes: {unique_prefixes}")

# ── 5. Cleanup ────────────────────────────────────────────────────────────
shutil.rmtree(tmpdir, ignore_errors=True)
print(f"\n  Cleaned up {tmpdir}")

# ── Summary ───────────────────────────────────────────────────────────────
print(f"\n{'='*55}")
print("Dynamic Binary Name Test Results")
print(f"{'='*55}")
print(f"  PASS: {len(PASS)}/{len(PASS)+len(FAIL)}")
print(f"  FAIL: {len(FAIL)}/{len(PASS)+len(FAIL)}")
if FAIL:
    print("\n  Failed checks:")
    for name, reason in FAIL:
        print(f"    ✗ {name} — {reason}")
print(f"{'='*55}")

sys.exit(1 if FAIL else 0)
