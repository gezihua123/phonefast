"""Build variant definitions — single source of truth for OCR build variants.

A Variant describes one OCR configuration (plain / cgo1 / apple / full):
the build tags, CGO setting, platform constraints, and output naming.

Adding a new variant = one row in VARIANTS below; builder.build_target reads
it data-driven, no if-else to update. This is the "保持拓展" point.
"""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class Variant:
    """One OCR build variant's full configuration.

    name:            plain / cgo1 / apple / full
    suffix:          output name suffix: "" / "-cgo1" / "-apple" / "-full"
    tags:            go build -tags values: [] / ["NO_OCR_MODELS"] / ["ocr_embed"]
    cgo:             desired CGO_ENABLED: "1" / "0"
    macos_only:      True = only meaningful on macOS (apple engine needs darwin&&cgo)
    platform_prefix: True = output name includes {goos}-{goarch} (plain/full,
                     needed for --all multi-platform archives); False = bare
                     phonefast<suffix> (cgo1/apple, macos_only so no collision)
    """
    name: str
    suffix: str
    tags: list[str] = field(default_factory=list)
    cgo: str = "1"
    macos_only: bool = False
    platform_prefix: bool = True


# The variant table. Add a row here to add a variant; build_target adapts.
VARIANTS: dict[str, Variant] = {
    # Standard build: ONNX + Tesseract + (Apple Vision on macOS via darwin&&cgo).
    # Loads system libonnxruntime; embeds PP-OCR models.
    "plain": Variant("plain", "", []),

    # CGO=1 explicit: same as plain but pinned CGO=1 so the apple engine is
    # guaranteed active on macOS (plain can lose it if FFmpeg missing → CGO=0).
    # macos_only because the whole point is the apple engine.
    "cgo1":  Variant("cgo1", "-cgo1", [], macos_only=True, platform_prefix=False),

    # Apple Vision only: no ONNX models embedded (NO_OCR_MODELS → DetModel/RecModel
    # nil → onnx engine returns ErrNotAvailable). Smallest macOS build.
    "apple": Variant("apple", "-apple", ["NO_OCR_MODELS"], macos_only=True, platform_prefix=False),

    # Self-contained: embeds ORT shared lib (ocr_embed) on top of plain.
    # Only darwin/arm64 is embeddable (platform.py Target.embeddable).
    "full":  Variant("full", "-full", ["ocr_embed"]),
}


def get(name: str) -> Variant:
    """Resolve a variant name. Raises KeyError on unknown variant."""
    if name not in VARIANTS:
        raise KeyError(f"Unknown variant: {name} (want one of {list(VARIANTS)})")
    return VARIANTS[name]
