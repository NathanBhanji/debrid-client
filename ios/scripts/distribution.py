"""Debrid-only signing and package checks; no credentials or profile values logged."""
import datetime
import hashlib
import os
import plistlib
import re
import subprocess
import tempfile
from pathlib import Path

TEAM = "ZTN995BU9H"
APP = "dev.bhanji.debrid"
GROUP = "group.dev.bhanji.debrid"
ASC_APP = "6830894823"
TEST_GROUP = "685db49e-7354-4445-9a53-8b0c7e2e8c03"
IDENTITY = "E6DD1985D71E06F3669F8B3C98F8C08E2C3202E7"
PROFILES = {APP: "dev.bhanji.debrid.appgroup.mobileprovision", APP + ".share": "dev.bhanji.debrid.share.appgroup.mobileprovision"}


def command(args, timeout=30):
    return subprocess.check_output(args, stderr=subprocess.DEVNULL, timeout=timeout)


def validate_profile(profile, bundle, identity=IDENTITY, now=None):
    if bundle not in PROFILES:
        raise ValueError("Unrecognized Debrid bundle")
    now = now or datetime.datetime.now(datetime.timezone.utc).replace(tzinfo=None)
    e = profile["Entitlements"]
    if profile["TeamIdentifier"] != [TEAM] or e.get("com.apple.developer.team-identifier") != TEAM:
        raise ValueError("Profile team mismatch")
    if e.get("application-identifier") != TEAM + "." + bundle:
        raise ValueError("Profile bundle mismatch")
    if e.get("com.apple.security.application-groups") != [GROUP]:
        raise ValueError("Required dedicated App Group missing or unexpected groups present")
    if e.get("get-task-allow") is not False or profile.get("ProvisionedDevices") or profile.get("ProvisionsAllDevices"):
        raise ValueError("Expected an App Store distribution profile")
    if profile["ExpirationDate"] <= now:
        raise ValueError("Distribution profile expired")
    if identity not in [hashlib.sha1(cert).hexdigest().upper() for cert in profile["DeveloperCertificates"]]:
        raise ValueError("Profile certificate mismatch")


def install_profiles(directory):
    identities = command(["security", "find-identity", "-v", "-p", "codesigning"]).decode()
    if IDENTITY not in identities:
        raise ValueError("Matching local Apple Distribution private identity unavailable")
    names = {}
    installed = Path.home() / "Library/Developer/Xcode/UserData/Provisioning Profiles"
    installed.mkdir(parents=True, exist_ok=True)
    for bundle, filename in PROFILES.items():
        source = directory / filename
        profile = plistlib.loads(command(["security", "cms", "-D", "-i", str(source)]))
        validate_profile(profile, bundle)
        if not re.fullmatch(r"[A-Fa-f0-9-]{36}", profile["UUID"]):
            raise ValueError("Invalid profile UUID")
        target = installed / (profile["UUID"] + ".mobileprovision")
        data = source.read_bytes()
        if target.exists() and target.read_bytes() != data:
            raise ValueError("Refusing to replace a different installed profile")
        target.write_bytes(data)
        os.chmod(target, 0o600)
        names[bundle] = profile["Name"]
    return names


def entitlements(bundle):
    return plistlib.loads(command(["codesign", "-d", "--entitlements", ":-", str(bundle)]))


def validate_signed_entitlements(e, identifier):
    if identifier not in PROFILES:
        raise ValueError("Unrecognized signed application bundle")
    if e.get("application-identifier") != TEAM + "." + identifier or e.get("com.apple.developer.team-identifier") != TEAM:
        raise ValueError("Actual signed team/bundle entitlement mismatch")
    if e.get("com.apple.security.application-groups") != [GROUP] or e.get("get-task-allow", False) is not False:
        raise ValueError("Actual signed App Group/distribution entitlement mismatch")
    keychain = e.get("keychain-access-groups", [])
    if identifier == APP and keychain != [TEAM + "." + APP]:
        raise ValueError("Expected exactly the app's private Keychain group")
    if identifier == APP + ".share" and keychain != []:
        raise ValueError("Extension must not access the app's or another app's Keychain group")


def verify_bundle(bundle, identifier, build, version, source):
    info = plistlib.loads((bundle / "Info.plist").read_bytes())
    if info["CFBundleIdentifier"] != identifier or info["CFBundleVersion"] != str(build) or info["CFBundleShortVersionString"] != version:
        raise ValueError("Bundle identity/version mismatch")
    if info.get("CFBundleSupportedPlatforms") != ["iPhoneOS"]:
        raise ValueError("Expected a physical-device platform bundle")
    if info["MinimumOSVersion"] != "27.0":
        raise ValueError("Unexpected deployment target")
    executable = bundle / info["CFBundleExecutable"]
    if command(["lipo", "-archs", str(executable)]).decode().strip() != "arm64":
        raise ValueError("Expected device arm64 executable")
    platform = command(["xcrun", "vtool", "-show-build", str(executable)]).decode()
    if not re.search(r"platform\s+IOS\b", platform):
        raise ValueError("Expected actual iOS Mach-O platform, not simulator arm64")
    command(["codesign", "--verify", "--strict", str(bundle)])
    with tempfile.TemporaryDirectory(prefix="debrid-signature-") as temporary:
        prefix = Path(temporary) / "certificate"
        command(["codesign", "-d", "--extract-certificates=" + str(prefix), str(bundle)])
        if hashlib.sha1(Path(str(prefix) + "0").read_bytes()).hexdigest().upper() != IDENTITY:
            raise ValueError("Actual signing certificate mismatch")
    if not (bundle / "PrivacyInfo.xcprivacy").is_file():
        raise ValueError("Privacy manifest missing from signed component")
    if identifier == APP + ".kit":
        return
    validate_signed_entitlements(entitlements(bundle), identifier)
    profile = plistlib.loads(command(["security", "cms", "-D", "-i", str(bundle / "embedded.mobileprovision")]))
    validate_profile(profile, identifier)
    if identifier == APP:
        if info.get("DebridSourceRevision") != source or info.get("ITSAppUsesNonExemptEncryption") is not False:
            raise ValueError("Release source/encryption metadata mismatch")
        if info.get("CFBundleIcons", {}).get("CFBundlePrimaryIcon", {}).get("CFBundleIconName") != "AppIcon":
            raise ValueError("App icon metadata missing")


def verify_app(app, build, version, source):
    components = [(app, APP), (app / "PlugIns/DebridShare.appex", APP + ".share"), (app / "Frameworks/DebridKit.framework", APP + ".kit")]
    for bundle, identifier in components:
        verify_bundle(bundle, identifier, build, version, source)
    if (components[1][0] / "Frameworks").exists():
        raise ValueError("Share Extension must use the app's embedded framework")
    command(["codesign", "--verify", "--deep", "--strict", str(app)])
