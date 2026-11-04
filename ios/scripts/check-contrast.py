#!/usr/bin/env python3
"""Check semantic text colors against both shipped surfaces in every appearance.

WCAG relative luminance uses linearized sRGB; ordinary text requires4.5:1.
This validates palette pairs, not composited system glass or arbitrary screenshots.
"""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1] / "Debrid/Assets.xcassets"


def colors(name):
    values = {}
    for item in json.loads((ROOT / (name + ".colorset") / "Contents.json").read_text())["colors"]:
        appearance = {v["appearance"]: v["value"] for v in item.get("appearances", [])}
        key = (appearance.get("luminosity", "light"), appearance.get("contrast", "normal"))
        rgba = item["color"]["components"]
        assert float(rgba["alpha"]) == 1, f"{name}: composite translucent text before measuring"
        values[key] = [float(rgba[v]) for v in ("red", "green", "blue")]
    assert set(values) == {(s, c) for s in ("light", "dark") for c in ("normal", "high")}, name
    return values


def luminance(rgb):
    return sum(w * (v / 12.92 if v <= 0.04045 else ((v + 0.055) / 1.055) ** 2.4)
               for w, v in zip((0.2126, 0.7152, 0.0722), rgb))


for foreground in ("PanelInk", "PanelMuted", "DeskAccent", "StageBlue", "StageAmber", "StageGreen", "DangerInk"):
    ratios = []
    for appearance, rgb in colors(foreground).items():
        for background in ("Chassis", "PanelFace"):
            low, high = sorted((luminance(rgb), luminance(colors(background)[appearance])))
            ratio = (high + 0.05) / (low + 0.05)
            assert ratio >= 4.5, f"{foreground}/{background}/{appearance}: {ratio:.2f}:1"
            ratios.append(ratio)
    print(f"{foreground}: minimum {min(ratios):.2f}:1 across eight surface/appearance pairs")
