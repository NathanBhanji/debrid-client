#!/usr/bin/env python3
"""Platform-independent checks for the metadata Apple validates during distribution."""
import json
import plistlib
import struct
from pathlib import Path

root = Path(__file__).resolve().parents[1]
icon = root / "Debrid/AppIcons.xcassets/AppIcon.appiconset"
contents = json.loads((icon / "Contents.json").read_text())
for image in contents["images"]:
    data = (icon / image["filename"]).read_bytes()
    assert data[:8] == b"\x89PNG\r\n\x1a\n"
    width, height, depth, color = struct.unpack(">IIBB", data[16:26])
    assert (width, height, depth, color) == (1024, 1024, 8, 2), "Use opaque RGB1024px icons"
manifest = plistlib.loads((root / "PrivacyInfo.xcprivacy").read_bytes())
assert manifest["NSPrivacyTracking"] is False and manifest["NSPrivacyTrackingDomains"] == []
assert manifest["NSPrivacyCollectedDataTypes"] == []
reasons = {item["NSPrivacyAccessedAPIType"]: set(item["NSPrivacyAccessedAPITypeReasons"]) for item in manifest["NSPrivacyAccessedAPITypes"]}
assert reasons == {"NSPrivacyAccessedAPICategoryUserDefaults": {"CA92.1"}, "NSPrivacyAccessedAPICategoryFileTimestamp": {"C617.1", "3B52.1"}}
print("Opaque icon and audited privacy declarations verified")
