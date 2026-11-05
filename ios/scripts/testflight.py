#!/usr/bin/env python3
"""Archive/export Debrid with validated dedicated signing. Upload is explicit and main-only."""
import argparse
import hashlib
import json
import os
import plistlib
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import zipfile
from pathlib import Path

from distribution import APP, ASC_APP, TEAM, install_profiles, verify_app
from workflow import acquire_lock

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--build-number", type=int, required=True)
parser.add_argument("--version", default="0.1.0")
parser.add_argument("--profiles-dir", type=Path, default=Path.home() / "Library/Application Support/Heimdall/debrid-apple-setup")
parser.add_argument("--allow-dirty", action="store_true", help="Permit local preview archives only")
parser.add_argument("--preflight", action="store_true")
parser.add_argument("--upload", action="store_true")
args = parser.parse_args()
if not 1 <= args.build_number <= 9999 or not re.fullmatch(r"\d{1,3}\.\d{1,3}\.\d{1,3}", args.version):
    parser.error("Use build1…9999 and a numeric three-part version")
if args.upload and (args.allow_dirty or args.preflight):
    parser.error("Upload cannot be dirty or preflight-only")
os.umask(0o077)
lock = acquire_lock(ROOT / ".artifacts/check.lock")
output = ROOT / ".artifacts/distribution"
if (ROOT / ".artifacts").is_symlink() or output.is_symlink():
    sys.exit("Refusing a symlinked distribution directory")
output.mkdir(parents=True, exist_ok=True)
marker = output / "ownership.json"
if any(output.iterdir()) and (not marker.exists() or json.loads(marker.read_text()) != {"bundle": APP, "generated_by": "ios/scripts/testflight.py"}):
    sys.exit("Refusing to clean an unrecognized distribution directory")
marker.write_text(json.dumps({"bundle": APP, "generated_by": "ios/scripts/testflight.py"}))


def git(*parts):
    return subprocess.check_output(["git", *parts], cwd=ROOT, text=True, timeout=30).strip()


def run(command, name, timeout):
    print(name + "…", flush=True)
    process = subprocess.Popen(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
    tail = bytearray()
    def drain():
        while chunk := process.stdout.read(65536):
            tail.extend(chunk)
            if len(tail) > 2_000_000:
                del tail[:-2_000_000]
    reader = threading.Thread(target=drain, daemon=True)
    reader.start()
    try:
        process.wait(timeout=timeout)
    except (subprocess.TimeoutExpired, KeyboardInterrupt):
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
        raise RuntimeError(name + " stopped after timeout/interruption")
    finally:
        reader.join(timeout=5)
        (output / (name + ".log")).write_bytes(tail)
    if process.returncode:
        raise RuntimeError(name + " failed; inspect its bounded local log")


source = git("rev-parse", "HEAD")
dirty = bool(git("status", "--porcelain"))
if dirty and not args.allow_dirty:
    sys.exit("Commit source changes before archiving, or use --allow-dirty for a local preview")
if args.upload and source != git("rev-parse", "origin/main"):
    sys.exit("Upload requires the reviewed origin/main commit checked out locally")
source_identifier = source + ("-dirty" if dirty else "")
api = None
if args.upload:
    from app_store import AppStore
    api = AppStore()
    api.verify_app()
    if api.builds(args.build_number, args.version):
        sys.exit("This build already exists in App Store Connect; choose a new build number")
profiles = install_profiles(args.profiles_dir)
print("Dedicated Debrid profiles, App Group and local distribution identity validated", flush=True)
if args.preflight:
    sys.exit(0)
for name in ("Debrid.xcarchive", "export"):
    path = output / name
    if path.exists():
        shutil.rmtree(path)
archive = output / "Debrid.xcarchive"
export = output / "export"
options = output / "ExportOptions.plist"
options.write_bytes(plistlib.dumps({"method": "app-store-connect", "destination": "export", "teamID": TEAM,
    "signingStyle": "manual", "signingCertificate": "Apple Distribution", "provisioningProfiles": profiles,
    "uploadSymbols": True, "manageAppVersionAndBuildNumber": False}))
run(["xcodegen", "generate"], "generate", 60)
run(["xcodebuild", "archive", "-project", "Debrid.xcodeproj", "-scheme", "Debrid", "-configuration", "Release",
    "-destination", "generic/platform=iOS", "-derivedDataPath", str(ROOT / ".build"), "-archivePath", str(archive),
    "CURRENT_PROJECT_VERSION=" + str(args.build_number), "MARKETING_VERSION=" + args.version,
    "DEBRID_SOURCE_REVISION=" + source_identifier], "archive", 1200)
verify_app(archive / "Products/Applications/Debrid.app", args.build_number, args.version, source_identifier)
print("Signed app, Share Extension and framework archive verified", flush=True)
run(["xcodebuild", "-exportArchive", "-archivePath", str(archive), "-exportPath", str(export), "-exportOptionsPlist", str(options)], "export", 600)
ipa = export / "Debrid.ipa"
with tempfile.TemporaryDirectory(prefix="debrid-export-check-") as temporary:
    with zipfile.ZipFile(ipa) as package:
        for entry in package.infolist():
            if entry.filename.startswith("/") or ".." in Path(entry.filename).parts:
                raise ValueError("Unsafe IPA member")
        package.extractall(temporary)
    verify_app(Path(temporary) / "Payload/Debrid.app", args.build_number, args.version, source_identifier)
result = {"bundle": APP, "asc_app_id": ASC_APP, "build": str(args.build_number), "version": args.version, "source": source_identifier, "dirty_preview": dirty,
    "ipa_sha256": hashlib.sha256(ipa.read_bytes()).hexdigest(), "signed_archive_verified": True, "export_verified": True,
    "uploaded": False, "physical_device_run": False}
(output / "validation.json").write_text(json.dumps(result, indent=2) + "\n")
print("Exported IPA signatures, entitlements, embedded profiles and arm64 components verified", flush=True)
if args.upload:
    api.verify_app()
    if api.builds(args.build_number, args.version):
        sys.exit("This build appeared during archiving; observe it before choosing another build number")
    run(["xcrun", "altool", "--validate-app", "-f", str(ipa), "-t", "ios", "--apiKey", api.key_id, "--apiIssuer", api.issuer], "validate", 600)
    run(["xcrun", "altool", "--upload-app", "-f", str(ipa), "-t", "ios", "--apiKey", api.key_id, "--apiIssuer", api.issuer], "upload", 1200)
    result["uploaded"] = True
    (output / "validation.json").write_text(json.dumps(result, indent=2) + "\n")
    print("Upload accepted; processing and explicit internal-group assignment still need verification", flush=True)
else:
    print("Preview export complete; no upload performed", flush=True)
