#!/usr/bin/env python3
"""Observe a known Debrid build; explicitly assign only a valid build to the dedicated internal group."""
import argparse
import json
import os
import re
import time
from pathlib import Path

from app_store import AppStore
from distribution import APP, ASC_APP, TEST_GROUP
from workflow import acquire_lock

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--build-number", type=int, required=True)
parser.add_argument("--version", default="0.1.0")
parser.add_argument("--wait-seconds", type=int, default=900)
parser.add_argument("--assign", action="store_true")
parser.add_argument("--upload-manifest", type=Path)
parser.add_argument("--tester-handoff", type=Path, default=Path.home() / "Library/Application Support/Heimdall/debrid-apple-setup/testflight-internal-group-private.json")
args = parser.parse_args()
if not 1 <= args.build_number <= 9999 or not 0 <= args.wait_seconds <= 1800:
    parser.error("Invalid build or wait bound")
os.umask(0o077)
root = Path(__file__).resolve().parents[1]
lock = acquire_lock(root / ".artifacts/check.lock")
if args.assign:
    manifest = json.loads((args.upload_manifest or root / ".artifacts/distribution/validation.json").read_text())
    if (manifest.get("bundle") != APP or manifest.get("asc_app_id") != ASC_APP or manifest.get("uploaded") is not True
        or manifest.get("build") != str(args.build_number) or manifest.get("version") != args.version
        or manifest.get("dirty_preview") is not False or not re.fullmatch(r"[0-9a-f]{40}", manifest.get("source", ""))
        or not re.fullmatch(r"[0-9a-f]{64}", manifest.get("ipa_sha256", ""))):
        raise ValueError("Assignment requires the matching successful clean upload manifest")
api = AppStore()
api.verify_app()
tester = api.verify_group()
# The dedicated setup handoff establishes the expected new app-scoped tester, avoiding other-app IDs.
handoff = args.tester_handoff
private = json.loads(handoff.read_text())
# Match only exact identifier fields in this app-specific, private provisioning artifact.
if tester != private.get("assigned_beta_tester_id"):
    raise ValueError("Internal tester differs from the verified provisioning handoff")
deadline = time.monotonic() + args.wait_seconds
build = None
while True:
    found = api.builds(args.build_number, args.version)
    if len(found) > 1:
        raise ValueError("Ambiguous build result")
    if found:
        build = found[0]
        state = build["attributes"]["processingState"]
        print("Build processing: " + state, flush=True)
        if state == "VALID":
            break
        if state in ("FAILED", "INVALID"):
            raise RuntimeError("Apple rejected build processing; inspect the app's processing diagnostics")
    else:
        print("Waiting for uploaded build to appear", flush=True)
    if time.monotonic() >= deadline:
        raise TimeoutError("Build processing observation reached its bound; no assignment performed")
    time.sleep(min(20, max(0, deadline - time.monotonic())))
identifier = build["id"]
if build["attributes"].get("expired"):
    raise ValueError("Build expired")
api.verify_audience(identifier, tester)
if args.assign:
    # What to Test contains no server address, credentials or test data.
    locales = api.call("GET", "/v1/builds/" + identifier + "/betaBuildLocalizations")["data"]
    text = "Connect to your HTTPS Debrid server and sign in with Pocket ID. For a tailnet-only server, connect this iPhone to Tailscale first. Review magnets or torrent files before adding. Test transfer progress, file selection, provider management, settings and sharing. Existing downloads stay on your server."
    english = next((item for item in locales if item["attributes"]["locale"] == "en-GB"), None)
    if english:
        api.call("PATCH", "/v1/betaBuildLocalizations/" + english["id"], {"data": {"type": "betaBuildLocalizations", "id": english["id"], "attributes": {"whatsNew": text}}})
    else:
        api.call("POST", "/v1/betaBuildLocalizations", {"data": {"type": "betaBuildLocalizations", "attributes": {"locale": "en-GB", "whatsNew": text}, "relationships": {"build": {"data": {"type": "builds", "id": identifier}}}}})
    current_groups = api.verify_audience(identifier, tester)
    if TEST_GROUP not in current_groups:
        api.call("POST", "/v1/betaGroups/" + TEST_GROUP + "/relationships/builds", {"data": [{"type": "builds", "id": identifier}]})
    details = api.call("GET", "/v1/builds/" + identifier + "/buildBetaDetail")["data"]
    if details["attributes"].get("autoNotifyEnabled") is not True:
        if TEST_GROUP not in api.verify_audience(identifier, tester):
            raise ValueError("Reviewed group assignment missing before notification")
        api.call("PATCH", "/v1/buildBetaDetails/" + details["id"], {"data": {"type": "buildBetaDetails", "id": details["id"], "attributes": {"autoNotifyEnabled": True}}})
groups = api.verify_audience(identifier, tester)
assigned = TEST_GROUP in groups
if args.assign and (not assigned or groups != {TEST_GROUP}):
    raise RuntimeError("Expected only the dedicated internal group after assignment")
if api.verify_group() != tester:
    raise RuntimeError("Tester membership changed during assignment")
result = {"bundle": APP, "app_id": ASC_APP, "build_id": identifier, "build": str(args.build_number), "version": args.version,
    "processing": "VALID", "internal_group": TEST_GROUP, "assigned": assigned, "tester_count": 1,
    "public_link": False, "physical_device_run": False}
output = root / ".artifacts/distribution/testflight.json"
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps(result), flush=True)
