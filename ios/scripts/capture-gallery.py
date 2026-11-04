#!/usr/bin/env python3
"""Capture the real SwiftUI gallery using the existing shared Debrid simulator."""
import argparse
import json
from pathlib import Path
import subprocess
import time
from workflow import acquire_lock, simulator_lock

ROOT = Path(__file__).resolve().parents[1]
SCREENS = ['connect', 'sign-in', 'transfers', 'detail', 'files', 'selection', 'add', 'share',
           'drafts', 'duplicate', 'uncertain', 'filters', 'providers', 'provider', 'account',
           'settings', 'categories', 'policy', 'organization', 'overrides', 'removal',
           'offline', 'empty', 'error', 'expired', 'diagnostics', 'cancellation', 'settings-conflict', 'tls', 'update', 'intake', 'account-removal']
p = argparse.ArgumentParser()
p.add_argument('--simulator', required=True)
p.add_argument('--screens', nargs='+', choices=SCREENS, default=SCREENS)
p.add_argument('--appearance', choices=['light', 'dark'], default='light')
a = p.parse_args()
checkout_lock = acquire_lock(ROOT / '.artifacts/check.lock')
device_lock = simulator_lock(a.simulator)

def run(*args, check=True, timeout=45):
    result = subprocess.run(['xcrun', 'simctl', *args], capture_output=True, text=True, timeout=timeout)
    if check and result.returncode:
        raise SystemExit(result.stderr[-2000:])
    return result

catalog = json.loads(run('list', 'devices', 'available', '--json').stdout)
device = next((d for items in catalog['devices'].values() for d in items if d['udid'] == a.simulator), None)
if not device or device['name'] != 'Debrid Shared iPhone':
    raise SystemExit('Select the existing Debrid Shared iPhone. No device will be created.')
if device['state'] != 'Shutdown':
    raise SystemExit('Simulator is already in use. Shut it down only after confirming its owner has finished.')
stamp = ROOT / '.artifacts/simulator-build.stamp'
inputs = [ROOT / 'project.yml', *ROOT.glob('Debrid/**/*.swift'), *ROOT.glob('DebridKit/**/*.swift'),
          *ROOT.glob('Debrid/Assets.xcassets/**/*.json')]
if not stamp.exists() or any(path.stat().st_mtime > stamp.stat().st_mtime for path in inputs):
    raise SystemExit('The simulator build is stale or unverified. Run scripts/check.py build successfully first.')
output = ROOT.parent / 'docs/design/ios/gallery' / a.appearance
output.mkdir(parents=True, exist_ok=True)
try:
    run('boot', a.simulator)
    run('bootstatus', a.simulator, '-b', timeout=180)
    run('ui', a.simulator, 'appearance', a.appearance)
    run('ui', a.simulator, 'content_size', 'large')
    run('status_bar', a.simulator, 'override', '--time', '9:41', '--batteryState', 'charged', '--batteryLevel', '100')
    run('install', a.simulator, str(ROOT / '.build/Build/Products/Debug-iphonesimulator/Debrid.app'))
    for index, screen in enumerate(a.screens):
        run('terminate', a.simulator, 'dev.bhanji.debrid', check=False)
        run('launch', a.simulator, 'dev.bhanji.debrid', '-DesignScreen', screen)
        time.sleep(4 if index == 0 else 2)
        run('io', a.simulator, 'screenshot', str(output / (screen + '.png')))
        print(f'Captured {screen} ({index + 1}/{len(a.screens)})', flush=True)
finally:
    # Only this UUID was booted by this process, under its cross-checkout device lock.
    run('terminate', a.simulator, 'dev.bhanji.debrid', check=False)
    run('status_bar', a.simulator, 'clear', check=False)
    run('ui', a.simulator, 'appearance', 'light', check=False)
    run('shutdown', a.simulator, check=False)
