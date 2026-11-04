"""Locks shared by local checks and captures. A UUID lock spans every checkout."""
import fcntl
from pathlib import Path
import uuid


def acquire_lock(path):
    path.parent.mkdir(parents=True, exist_ok=True)
    handle = path.open("a")
    try:
        fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        handle.close()
        raise SystemExit("Debrid operation already owns this checkout or simulator; wait for it to finish.")
    return handle


def simulator_lock(device):
    identifier = str(uuid.UUID(device))
    return acquire_lock(Path.home() / "Library/Caches/dev.bhanji.debrid/locks" / (identifier + ".lock"))
