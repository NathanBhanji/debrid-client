#!/usr/bin/env python3
"""Bounded checks. No implicit simulator creation, downloads or signing changes."""
import argparse
import os
from pathlib import Path
import signal
import subprocess
import sys
import threading
from workflow import acquire_lock, simulator_lock

ROOT = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument("mode", choices=["build", "test", "device"])
p.add_argument("--simulator", help="Existing, explicitly selected simulator UUID (required for test)")
a = p.parse_args()
if a.mode == "test" and not a.simulator:
    p.error("test requires --simulator UUID; reuse the Debrid shared device")
artifacts = ROOT / ".artifacts"
artifacts.mkdir(exist_ok=True)
lock = acquire_lock(artifacts / "check.lock")
sim_lock = simulator_lock(a.simulator) if a.mode == "test" else None

def run(command, name):
    path = artifacts / (name + ".log")
    # Drain continuously while retaining only the final 2 MB in memory and on disk.
    tail = bytearray()
    process = subprocess.Popen(command, cwd=ROOT, stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, start_new_session=True)

    def drain():
        while chunk := process.stdout.read(65536):
            tail.extend(chunk)
            if len(tail) > 2_000_000:
                del tail[:-2_000_000]

    reader = threading.Thread(target=drain, daemon=True)
    reader.start()
    stopped = False
    try:
        process.wait(timeout=900)
    except (subprocess.TimeoutExpired, KeyboardInterrupt):
        stopped = True
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
    reader.join(timeout=5)
    path.write_bytes(tail)
    print(tail[-3000:].decode(errors="replace"))
    if stopped:
        sys.exit("Check stopped after timeout/interruption: " + str(path))
    if process.returncode:
        sys.exit(process.returncode)

run(["xcodegen", "generate"], "generate")
command = ["xcodebuild", "-project", "Debrid.xcodeproj", "-scheme", "Debrid", "-configuration", "Debug",
           "-derivedDataPath", str(ROOT / ".build"), "CODE_SIGNING_ALLOWED=NO"]
if a.mode == "test":
    command += ["-destination", "platform=iOS Simulator,id=" + a.simulator, "-parallel-testing-enabled", "NO", "-collect-test-diagnostics", "never", "test"]
else:
    command += ["-destination", "generic/platform=iOS" if a.mode == "device" else "generic/platform=iOS Simulator", "build"]
run(command, a.mode)

if a.mode in ("build", "test"):
    (artifacts / "simulator-build.stamp").write_text("Successful simulator build\n")
