#!/usr/bin/env python3
"""Real OS cold/warm magnet delivery and actual native review captures on the owned shared device."""
import argparse
import json
import os
import signal
import threading
import plistlib
from pathlib import Path
import re
import shutil
import subprocess
import time
import uuid
from workflow import acquire_lock, simulator_lock

ROOT = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument("--simulator", required=True)
p.add_argument("--capture", action="store_true")
a = p.parse_args()
artifacts = ROOT / ".artifacts"
lock = acquire_lock(artifacts / "check.lock")
sim_lock = simulator_lock(a.simulator)
namespace = str(uuid.uuid4()).upper()
config = None
old_config = None
store = None
started = False
config_touched = False
helper = None
helper_reader = None
helper_tail = bytearray()

def run(*args, check=True):
    result = subprocess.run(["xcrun", "simctl", *args], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=45)
    if check and result.returncode:
        raise RuntimeError("Simulator operation failed: " + args[0])
    return result

def start_open_confirmation_helper():
    global helper, helper_reader
    build_command = ["xcodebuild", "-project", str(ROOT / "Debrid.xcodeproj"), "-scheme", "Debrid", "-configuration", "Debug", "-derivedDataPath", str(ROOT / ".build"), "CODE_SIGN_IDENTITY=-", "CODE_SIGNING_ALLOWED=YES", "DEVELOPMENT_TEAM=", "AppIdentifierPrefix=ZTN995BU9H.", "-destination", "platform=iOS Simulator,id="+a.simulator, "build-for-testing"]
    build = subprocess.Popen(build_command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
    try:
        output, _ = build.communicate(timeout=90)
    except (subprocess.TimeoutExpired, KeyboardInterrupt):
        os.killpg(build.pid, signal.SIGKILL); build.communicate(); raise
    (artifacts / "magnet-delivery-build.log").write_bytes(output[-2_000_000:])
    assert build.returncode == 0, "Could not prepare native confirmation helper"
    products = ROOT / ".build/Build/Products"
    runs = [path for path in products.glob("*iphonesimulator*.xctestrun")]
    assert runs, "No native simulator test manifest"
    source = max(runs, key=lambda path: path.stat().st_mtime)
    document = plistlib.loads(source.read_bytes())
    targets = [target for configuration in document.get("TestConfigurations", []) for target in configuration.get("TestTargets", [])]
    if not targets:
        targets = [value for value in document.values() if isinstance(value, dict) and "TestBundlePath" in value]
    selected = 0
    for target in targets:
        target.pop("OnlyTestIdentifiers", None); target.pop("SkipTestIdentifiers", None)
        if target.get("BlueprintName") == "DebridUITests" or target.get("IsUITestBundle"):
            target.setdefault("EnvironmentVariables", {})["DEBRID_SYSTEM_OPEN_HELPER"] = "1"
            target.setdefault("TestingEnvironmentVariables", {})["DEBRID_SYSTEM_OPEN_HELPER"] = "1"
            target["OnlyTestIdentifiers"] = ["SystemOpenConfirmationTests/testConfirmOnlyDebridAppOpenPrompts"]
            selected += 1
    assert selected, "No UI test target in native manifest"
    manifest = products / "DebridOpenHelper.xctestrun"
    manifest.write_bytes(plistlib.dumps(document)); manifest.chmod(0o600)
    command = ["xcodebuild", "-xctestrun", str(manifest), "-derivedDataPath", str(ROOT / ".build"), "-destination", "platform=iOS Simulator,id="+a.simulator, "-parallel-testing-enabled", "NO", "-collect-test-diagnostics", "never", "-only-testing:DebridUITests/SystemOpenConfirmationTests", "test-without-building"]
    helper = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
    ready = threading.Event()
    def drain():
        while line := helper.stdout.readline():
            helper_tail.extend(line)
            if len(helper_tail) > 2_000_000:
                del helper_tail[:-2_000_000]
            if b"DEBRID_OPEN_HELPER_READY" in line:
                ready.set()
    helper_reader = threading.Thread(target=drain, daemon=True); helper_reader.start()
    assert ready.wait(timeout=45), "Native app-open confirmation helper did not start"

def finish_helper():
    if helper is None:
        return
    if helper.poll() is None:
        os.killpg(helper.pid, signal.SIGTERM)
        try:
            helper.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(helper.pid, signal.SIGKILL); helper.wait(timeout=5)
    if helper_reader is not None:
        helper_reader.join(timeout=5)
    (artifacts / "magnet-delivery-helper.log").write_bytes(helper_tail)
    runner = ROOT / ".build/Build/Products/Debug-iphonesimulator/DebridUITests-Runner.app/Info.plist"
    if runner.exists():
        identifier = plistlib.loads(runner.read_bytes())["CFBundleIdentifier"]
        run("terminate", a.simulator, identifier, check=False)

def configuration(mode=None):
    data = {"id": namespace}
    if mode:
        data["mode"] = mode
    config.parent.mkdir(parents=True, exist_ok=True)
    config.write_text(json.dumps(data))

def rows():
    try:
        return json.loads((store / "inbox.json").read_text())["items"]
    except FileNotFoundError:
        return []

def wait_saved(uri):
    for _ in range(200):
        found = [row for row in rows() if row["rawURI"] == uri]
        if len(found) == 1:
            return found[0]
        time.sleep(0.1)
    received = rows()
    print("Delivery diagnostic:", {"receivedCount": len(received), "expectedBytes": len(uri.encode()), "receivedBytes": [len(row["rawURI"].encode()) for row in received]}, flush=True)
    if a.capture:
        screenshots = artifacts / "screenshots"; screenshots.mkdir(exist_ok=True)
        run("io", a.simulator, "screenshot", str(screenshots / "pending-delivery-failure.png"), check=False)
    raise AssertionError("Expected original URI was not received/preserved")

def running_pid():
    output = run("spawn", a.simulator, "launchctl", "list").stdout.decode()
    matches = [int(line.split()[0]) for line in output.splitlines() if "UIKitApplication:dev.bhanji.debrid[" in line and line.split()[0].isdigit()]
    assert len(matches) <= 1, "Expected at most one Debrid process"
    return matches[0] if matches else None

def pid():
    value = running_pid(); assert value is not None, "Debrid did not launch"; return value

def stop_app():
    run("terminate", a.simulator, "dev.bhanji.debrid", check=False)
    for _ in range(100):
        if running_pid() is None:
            return
        time.sleep(0.05)
    raise AssertionError("Debrid did not terminate")

def uri(name):
    return "magnet:?xt=urn:btih:" + "0" * 40 + "&dn=" + name + "&tr=https%3a%2f%2ftracker.example.test%2fa%3Fkey%3Dfixture%26x%3Dy&tr=udp%3A%2F%2Ftracker.example.test%3A80&unknown=a%2526b"

try:
    devices = json.loads(run("list", "devices", "available", "--json").stdout)["devices"]
    device = next(value for values in devices.values() for value in values if value["udid"] == a.simulator)
    assert device["name"] == "Debrid Shared iPhone", "Use only the owned shared Debrid simulator"
    stamp = artifacts / "simulator-build.stamp"
    newest = max(path.stat().st_mtime for path in list((ROOT / "Debrid").glob("*.swift")) + list((ROOT / "DebridKit").glob("*.swift")) + [ROOT / "project.yml"])
    assert stamp.exists() and stamp.stat().st_mtime >= newest, "Build/test the latest simulator app first"
    started = True
    if a.capture:
        for name in ["files-native-light.png", "files-native-dark.png", "file-folder-native-light.png", "file-folder-native-dark.png", "pending-delivery-failure.png"]:
            (artifacts / "screenshots" / name).unlink(missing_ok=True)
    if device["state"] == "Shutdown":
        run("boot", a.simulator)
    run("bootstatus", a.simulator, "-b")
    run("install", a.simulator, str(ROOT / ".build/Build/Products/Debug-iphonesimulator/Debrid.app"))
    run("terminate", a.simulator, "dev.bhanji.debrid", check=False)
    data = Path(run("get_app_container", a.simulator, "dev.bhanji.debrid", "data").stdout.decode().strip())
    config = data / "Library/Application Support/DebridIntakeTest.json"
    if config.exists():
        assert config.stat().st_size <= 1024, "Unexpected test configuration size"
        old_config = config.read_bytes()
    config_touched = True
    configuration()
    run("launch", a.simulator, "dev.bhanji.debrid")
    group = Path(run("get_app_container", a.simulator, "dev.bhanji.debrid", "group.dev.bhanji.debrid").stdout.decode().strip())
    assert group.is_absolute() and group.is_dir(), "Unexpected App Group container"
    store = group / ("InboxTests-" + namespace)
    for _ in range(100):
        if store.is_dir():
            break
        time.sleep(0.1)
    assert store.is_dir(), "Debug test namespace was not activated; no fixture URLs sent"
    stop_app()
    start_open_confirmation_helper()
    cold = uri("Cold%20fixture")
    run("openurl", a.simulator, cold)
    wait_saved(cold)
    cold_pid = pid()
    warm = uri("Warm%20fixture")
    run("openurl", a.simulator, warm)
    wait_saved(warm)
    assert pid() == cold_pid, "Warm delivery restarted the process"
    run("openurl", a.simulator, warm)
    time.sleep(0.5)
    assert len(rows()) == 2, "Duplicate delivery created another pending item"
    run("openurl", a.simulator, "dev.bhanji.debrid://auth/callback?code=fixture&state=fixture")
    time.sleep(0.3)
    assert len(rows()) == 2, "Auth callback entered the magnet inbox"
    print("Cold/warm OS delivery, same-process warm routing, original encoding, duplicate handling and auth-scheme isolation passed.", flush=True)
    if a.capture:
        screenshots = artifacts / "screenshots"
        screenshots.mkdir(exist_ok=True)
        for name in ["files-native-light.png", "files-native-dark.png", "file-folder-native-light.png", "file-folder-native-dark.png"]:
            (screenshots / name).unlink(missing_ok=True)
        run("ui", a.simulator, "content_size", "large")
        run("status_bar", a.simulator, "override", "--time", "9:41", "--batteryState", "charged", "--batteryLevel", "100")
        for mode in ["list", "review"]:
            for appearance in ["light", "dark"]:
                stop_app()
                configuration(mode)
                run("ui", a.simulator, "appearance", appearance)
                value = uri("Incoming%20fixture%20" + mode + "%20" + appearance)
                run("openurl", a.simulator, value)
                wait_saved(value)
                time.sleep(12)
                run("io", a.simulator, "screenshot", str(screenshots / ("pending-" + mode + "-" + appearance + ".png")))
                print("Captured native", mode, appearance, flush=True)
    try:
        helper.wait(timeout=120)
    except subprocess.TimeoutExpired:
        raise AssertionError("Native confirmation helper exceeded its deadline")
    assert helper.returncode == 0, "Native confirmation helper failed"
finally:
    finish_helper()
    if started:
        run("terminate", a.simulator, "dev.bhanji.debrid", check=False)
        run("ui", a.simulator, "appearance", "light", check=False)
        run("status_bar", a.simulator, "clear", check=False)
        run("shutdown", a.simulator, check=False)
        if config_touched:
            if old_config is None:
                config.unlink(missing_ok=True)
            else:
                config.write_bytes(old_config)
        if store is not None and store.is_dir():
            shutil.rmtree(store)
