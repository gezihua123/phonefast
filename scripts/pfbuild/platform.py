"""Platform matrix — single source of truth for all os/arch → toolchain mappings.

Replaces build.sh's os_arch_to_zig + os_arch_to_ffmpeg_target and
download-ocr-models.sh's resolve_target with one table.
"""
from __future__ import annotations

import subprocess
from dataclasses import dataclass

# Lib extension per OS (for deriving lib_name / embed_name).
_LIBEXT = {"darwin": "dylib", "linux": "so", "windows": "dll"}


@dataclass(frozen=True)
class Target:
    """One build target's full toolchain mapping."""
    goos: str
    goarch: str
    zig_target: str        # aarch64-macos-none / x86_64-linux-gnu / x86_64-windows-gnu
    ffmpeg_target: str     # aarch64-darwin / x86_64-linux-gnu / ...
    ort_suffix: str        # osx-arm64 / linux-x64 / win-x64 (ORT release asset suffix)
    default_release: bool = True   # included in --all builds (darwin/amd64 = False)

    @property
    def ext(self) -> str:
        """Binary extension: .exe on windows, empty otherwise."""
        return ".exe" if self.goos == "windows" else ""

    @property
    def lib_name(self) -> str:
        """The ORT shared library filename for this platform."""
        prefix = "" if self.goos == "windows" else "lib"
        return f"{prefix}onnxruntime.{_LIBEXT[self.goos]}"

    @property
    def embed_name(self) -> str:
        """The embedded lib filename in assets/ocr/ (libonnxruntime-<goos>-<goarch>.<ext>)."""
        return f"libonnxruntime-{self.goos}-{self.goarch}.{_LIBEXT[self.goos]}"

    @property
    def embeddable(self) -> bool:
        """True if a lib_<goos>_<goarch>.go embed file exists for this target.

        Currently only darwin/arm64 has lib_darwin_arm64.go. This is the
        single source of truth for "-full builds make sense for this target";
        builder.py checks it to avoid silently producing a -full binary that's
        byte-identical to plain (ocr_embed tag → lib_nolib.go → RuntimeLib=nil).
        """
        return self.goos == "darwin" and self.goarch == "arm64"


# The full matrix. download-ocr-models.sh's resolve_target supports all 5;
# build's --all uses the default_release=True entries (darwin/amd64 excluded).
TARGETS: dict[str, Target] = {
    "darwin/arm64":  Target("darwin", "arm64",  "aarch64-macos-none",  "aarch64-darwin",   "osx-arm64"),
    "darwin/amd64":  Target("darwin", "amd64",  "x86_64-macos-none",   "x86_64-darwin",    "osx-x86_64",  default_release=False),
    "linux/amd64":   Target("linux",  "amd64",  "x86_64-linux-gnu",    "x86_64-linux-gnu", "linux-x64"),
    "linux/arm64":   Target("linux",  "arm64",  "aarch64-linux-gnu",   "aarch64-linux-gnu","linux-aarch64"),
    "windows/amd64": Target("windows", "amd64", "x86_64-windows-gnu",  "x86_64-windows-gnu","win-x64"),
}

# Derived views (no hand-maintained parallel lists).
BUILD_PLATFORMS_ALL = [k for k, t in TARGETS.items() if t.default_release]

def _filter_map() -> dict[str, list[str]]:
    """Group default_release targets by goos for --macos/--linux/--windows."""
    m: dict[str, list[str]] = {}
    for k, t in TARGETS.items():
        if t.default_release:
            m.setdefault(t.goos, []).append(k)
    return m

FILTER_MAP = _filter_map()


def resolve(target_str: str) -> Target:
    """Resolve a target string to a Target.

    "host" or "" → detect via `go env GOOS`/`GOARCH` (with platform fallback).
    "goos/goarch" → look up TARGETS.
    Raises KeyError on unknown target.
    """
    if target_str in ("host", ""):
        target_str = f"{_go_env('GOOS')}/{_go_env('GOARCH')}"
    if target_str not in TARGETS:
        raise KeyError(f"Unsupported target: {target_str} (want goos/goarch, e.g. darwin/arm64)")
    return TARGETS[target_str]


def host_target() -> Target:
    """The host platform's Target."""
    return resolve("host")


def _go_env(key: str) -> str:
    """Get GOOS/GOARCH via `go env` (respects user-set env vars), with fallback."""
    try:
        out = subprocess.run(["go", "env", key], capture_output=True, text=True).stdout.strip()
        if out:
            return out
    except Exception:
        pass
    # Fallback to platform detection.
    import platform as _p
    goos = {"Darwin": "darwin", "Linux": "linux"}.get(_p.system(), "windows")
    if key == "GOOS":
        return goos
    machine = _p.machine().lower()
    return "arm64" if machine in ("arm64", "aarch64") else "amd64"
