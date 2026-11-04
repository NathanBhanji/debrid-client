#!/usr/bin/env python3
"""Two real host processes exercise the production POSIX/atomic storage sources. No simulator."""
import concurrent.futures
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import tempfile
import time
from workflow import acquire_lock

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / ".artifacts"
ARTIFACTS.mkdir(exist_ok=True)
lock = acquire_lock(ARTIFACTS / "check.lock")
binary = ARTIFACTS / "inbox-process-probe"
compiler = subprocess.Popen(["xcrun", "--sdk", "macosx", "swiftc", "-parse-as-library", "-swift-version", "6", "-strict-concurrency=complete", "-module-cache-path", str(ROOT / ".build/inbox-host-modules"), str(ROOT / "DebridKit/MagnetLink.swift"), str(ROOT / "DebridKit/ServerAddress.swift"), str(ROOT / "DebridKit/TorrentFile.swift"), str(ROOT / "DebridKit/APIDTOs.swift"), str(ROOT / "DebridKit/SubmissionIntent.swift"), str(ROOT / "DebridKit/PendingInbox.swift"), str(ROOT / "scripts/fixtures/PendingInboxProbe.swift"), "-o", str(binary)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
try:
    compiler_output, _ = compiler.communicate(timeout=90)
except (subprocess.TimeoutExpired, KeyboardInterrupt):
    os.killpg(compiler.pid, signal.SIGTERM)
    try:
        compiler.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        os.killpg(compiler.pid, signal.SIGKILL); compiler.communicate()
    raise SystemExit("Inbox host compilation stopped after timeout/interruption")
(ARTIFACTS / "inbox-process-build.log").write_bytes(compiler_output[-2_000_000:])
if compiler.returncode:
    raise SystemExit(compiler_output[-3000:].decode(errors="replace"))
children = []

def start(command, directory):
    child = subprocess.Popen([str(binary), command, str(directory)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    children.append(child)
    return child

def call(command, directory, payload=None):
    child = start(command, directory)
    try:
        output, error = child.communicate(json.dumps(payload or {}).encode(), timeout=8)
    except subprocess.TimeoutExpired:
        os.killpg(child.pid, signal.SIGKILL); child.communicate(); raise
    if child.returncode or error:
        raise AssertionError("Inbox probe failed")
    return json.loads(output)

def ready(child):
    readable, _, _ = select.select([child.stdout], [], [], 8)
    assert readable, "Probe did not become ready"
    return json.loads(child.stdout.readline())

with tempfile.TemporaryDirectory(prefix="debrid-inbox-process-") as temp:
    try:
        directory = Path(temp) / "store"
        uri = "magnet:?xt=urn:btih:" + "0" * 40 + "&dn=Fixture"
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(lambda _: call("enqueue", directory, {"uri": uri}), range(2)))
        assert len({value["id"] for value in results}) == 1
        assert sum(value["duplicate"] for value in results) == 1
        torrent = json.loads((ROOT.parent / "testdata/ios-api/torrents.json").read_text())[0]["data"]
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            copies = list(pool.map(lambda _: call("enqueueTorrent", directory, {"data": torrent}), range(2)))
        assert len({value["id"] for value in copies}) == 1
        assert sum(value["duplicate"] for value in copies) == 1
        assert len(list(directory.glob("*.torrent-payload"))) == 1
        identifier = results[0]["id"]
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            claims = list(pool.map(lambda _: call("claim", directory, {"id": identifier}), range(2)))
        assert sum("token" in value for value in claims) == 1
        assert sum(value.get("error") == "busy" for value in claims) == 1
        winner = next(value for value in claims if "token" in value)
        assert call("release", directory, winner)["count"] == 2
        holder = start("hold", directory); ready(holder)
        assert call("snapshot", directory).get("error") == "busy"
        os.killpg(holder.pid, signal.SIGKILL); holder.communicate(timeout=3)
        assert call("snapshot", directory)["count"] == 2
        crash_directory = Path(temp) / "crash"
        writer = start("flood", crash_directory); ready(writer); time.sleep(0.01)
        os.killpg(writer.pid, signal.SIGKILL); writer.communicate(timeout=3)
        count = call("snapshot", crash_directory)["count"]
        assert 1 <= count <= 20
        assert not list(crash_directory.glob(".pending-write-*.tmp"))
        print("Two-process magnet/torrent-copy duplicate and claim exclusion, bounded busy response, killed-lock recovery and writer process-death recovery passed.")
    finally:
        for child in children:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGKILL)
            child.communicate(timeout=3)
