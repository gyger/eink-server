#!/usr/bin/env python3
"""Compare candidate 4-bit pixel preparation with a captured VSS raster.

This intentionally uses only Pillow and the Python standard library so it can
run on the development machine without NumPy. The reference PNG must be a
decoded, full-screen VSS framebuffer (one grayscale byte per pixel).
"""

from __future__ import annotations

import argparse
import json
import math
from pathlib import Path

from PIL import Image, ImageChops


LEVEL = 17


def grayscale_pixels(image: Image.Image) -> list[int]:
    """Use the same RGB weights and rounding as internal/imageproc."""
    rgb = image.convert("RGB")
    return [round(0.299 * r + 0.587 * g + 0.114 * b) for r, g, b in rgb.get_flattened_data()]


def quantize(value: float) -> int:
    return min(255, max(0, math.floor(value / LEVEL + 0.5) * LEVEL))


def prepare(source: list[int], gamma: float, normalize: bool, gamma_mode: str) -> list[int]:
    if gamma_mode == "power":
        values = [255.0 * ((value / 255.0) ** gamma) for value in source]
    elif gamma_mode == "vss-linear":
        # Reconstructed from generateGammaLUT(float) in the VSS engine. Despite
        # its name, this is a clipped linear division, not pow(value, gamma).
        values = [min(255.0, max(0.0, value / gamma)) for value in source]
    else:
        raise ValueError(f"unknown gamma mode: {gamma_mode}")
    if normalize:
        low, high = min(values), max(values)
        if high > low:
            values = [(value - low) * 255.0 / (high - low) for value in values]
    return [quantize(value) for value in values]


def prepare_native_vss(source: list[int], gamma: float) -> list[int]:
    """Reproduce the grayscale path found in VSS beautify.cpp.

    generateGammaLUT truncates pixel/gamma. The first cvConvertScale maps the
    LUT result into an integer 0..15 image. VSS then measures that integer
    image's actual extrema and maps them back to 0..255.
    """
    lut = [min(255, max(0, math.trunc(value / gamma))) for value in range(256)]
    reduced = [round(lut[value] * 15.0 / 255.0) for value in source]
    low, high = min(reduced), max(reduced)
    if low == high:
        low, high = 0, 15
    expanded = [round((value - low) * 255.0 / (high - low)) for value in reduced]
    return [quantize(value) for value in expanded]


def suppress_antialias(source: list[int], width: int, height: int) -> list[int]:
    output = source.copy()
    for y in range(height):
        for x in range(width):
            neighborhood = [
                source[min(height - 1, max(0, y + dy)) * width + min(width - 1, max(0, x + dx))]
                for dy in (-1, 0, 1)
                for dx in (-1, 0, 1)
            ]
            low, high = min(neighborhood), max(neighborhood)
            if high - low >= 128:
                value = source[y * width + x]
                output[y * width + x] = low if value - low <= high - value else high
    return output


def packed(pixels: list[int]) -> bytes:
    if len(pixels) % 2:
        raise ValueError("packed 4-bit comparison requires an even pixel count")
    # PV3 Encoding 4 stores the left pixel in the low nibble and the right
    # pixel in the high nibble.
    return bytes((pixels[i + 1] // LEVEL) << 4 | pixels[i] // LEVEL for i in range(0, len(pixels), 2))


def metrics(candidate: list[int], reference: list[int]) -> dict[str, int | float]:
    differences = [abs(a - b) for a, b in zip(candidate, reference)]
    squared = sum(value * value for value in differences)
    candidate_bytes, reference_bytes = packed(candidate), packed(reference)
    return {
        "different_pixels": sum(value != 0 for value in differences),
        "different_pixel_percent": 100.0 * sum(value != 0 for value in differences) / len(differences),
        "mean_absolute_error": sum(differences) / len(differences),
        "root_mean_square_error": math.sqrt(squared / len(differences)),
        "maximum_error": max(differences),
        "different_packed_bytes": sum(a != b for a, b in zip(candidate_bytes, reference_bytes)),
    }


def oriented(image: Image.Image, name: str) -> Image.Image:
    transpose = Image.Transpose
    operations = {
        "identity": None,
        "rotate-180": transpose.ROTATE_180,
        "flip-horizontal": transpose.FLIP_LEFT_RIGHT,
        "flip-vertical": transpose.FLIP_TOP_BOTTOM,
    }
    operation = operations[name]
    return image.copy() if operation is None else image.transpose(operation)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="PNG submitted to VSS")
    parser.add_argument("reference", type=Path, help="decoded full-screen VSS PNG")
    parser.add_argument("--output", type=Path, default=Path("vss-comparison"))
    parser.add_argument("--assert-exact", action="store_true", help="fail unless a candidate is byte-identical")
    args = parser.parse_args()

    source_image = Image.open(args.source)
    reference_image = Image.open(args.reference).convert("L")
    if source_image.size != reference_image.size:
        parser.error(f"dimension mismatch: source={source_image.size}, reference={reference_image.size}")

    reference = [quantize(value) for value in reference_image.get_flattened_data()]
    gammas = [0.7, 0.8, 0.9, 1.0, 1.1, 1.2, 1.3, 1.4, 1.5]
    results = []
    best_pixels: list[int] | None = None
    best_key: tuple[int | float, int | float] | None = None
    for orientation in ("identity", "rotate-180", "flip-horizontal", "flip-vertical"):
        source = grayscale_pixels(oriented(source_image, orientation))
        name = f"{orientation}-native-vss-gamma-1.1"
        candidate = prepare_native_vss(source, 1.1)
        result = {"candidate": name, **metrics(candidate, reference)}
        results.append(result)
        key = (result["different_pixels"], result["mean_absolute_error"])
        if best_key is None or key < best_key:
            best_key = key
            best_pixels = candidate
        name = f"{orientation}-crisp-native-vss-gamma-1.1"
        candidate = prepare_native_vss(suppress_antialias(source, *source_image.size), 1.1)
        result = {"candidate": name, **metrics(candidate, reference)}
        results.append(result)
        key = (result["different_pixels"], result["mean_absolute_error"])
        if best_key is None or key < best_key:
            best_key = key
            best_pixels = candidate
        for gamma_mode in ("power", "vss-linear"):
            for normalize in (False, True):
                for gamma in gammas:
                    name = f"{orientation}-{gamma_mode}-gamma-{gamma:.1f}" + ("-normalize" if normalize else "")
                    candidate = prepare(source, gamma, normalize, gamma_mode)
                    result = {"candidate": name, **metrics(candidate, reference)}
                    results.append(result)
                    key = (result["different_pixels"], result["mean_absolute_error"])
                    if best_key is None or key < best_key:
                        best_key = key
                        best_pixels = candidate

    results.sort(key=lambda result: (result["different_pixels"], result["mean_absolute_error"]))
    best = results[0]
    assert best_pixels is not None
    args.output.mkdir(parents=True, exist_ok=True)
    size = source_image.size
    best_image = Image.new("L", size)
    best_image.putdata(best_pixels)
    best_image.save(args.output / "best-candidate.png")
    reference_quantized = Image.new("L", size)
    reference_quantized.putdata(reference)
    ImageChops.difference(best_image, reference_quantized).save(args.output / "difference.png")
    report = {
        "source": str(args.source),
        "reference": str(args.reference),
        "dimensions": list(size),
        "best": best,
        "results": results,
    }
    (args.output / "report.json").write_text(json.dumps(report, indent=2) + "\n")

    print(json.dumps({"best": best, "output": str(args.output)}, indent=2))
    if args.assert_exact and best["different_packed_bytes"] != 0:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
