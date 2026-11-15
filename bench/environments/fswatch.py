# Workspace activity observer. Runs in a trusted helper container that mounts
# the workspace volume read-only at /w, with no network and no capability but
# DAC_READ_SEARCH. inotify is per inode, so it sees the writes the agent makes
# through its own mount of the same volume. It prints one JSON line per file
# change on stdout; the bench stores them.
#
# A new file written and closed is one create with its final size; a write to
# an existing file is one modify when it is closed, or after 2 s if it stays
# open. Git internals and interpreter caches are skipped.
#
# argv[1] caps the number of events printed; the rest are counted as dropped.
import ctypes
import json
import os
import select
import signal
import struct
import sys
import time

ROOT = "/w"
MAX_EVENTS = int(sys.argv[1]) if len(sys.argv) > 1 else 20000
MAX_WATCHES = 16384
SKIP = {".git", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "node_modules", ".cache"}
IN_MODIFY, IN_CLOSE_WRITE, IN_MOVED_FROM, IN_MOVED_TO = 0x2, 0x8, 0x40, 0x80
IN_CREATE, IN_DELETE, IN_DELETE_SELF, IN_IGNORED = 0x100, 0x200, 0x400, 0x8000
IN_Q_OVERFLOW, IN_ISDIR, IN_ONLYDIR, IN_DONT_FOLLOW = 0x4000, 0x40000000, 0x01000000, 0x02000000
MASK = IN_MODIFY | IN_CLOSE_WRITE | IN_MOVED_FROM | IN_MOVED_TO | IN_CREATE | IN_DELETE | IN_DELETE_SELF | IN_ONLYDIR | IN_DONT_FOLLOW

libc = ctypes.CDLL(None, use_errno=True)
fd = libc.inotify_init1(os.O_NONBLOCK | os.O_CLOEXEC)
out = sys.stdout
paths = {}  # wd -> relative folder path ("" is the root)
wds = {}  # relative folder path -> wd
pending = {}  # relative path -> [op, first change time]
emitted = 0
dropped = 0
notes = []
stopping = False


def skipped(rel):
    if rel.endswith(".pyc"):
        return True
    return any(c in SKIP for c in rel.split("/"))


def size(rel):
    try:
        return os.lstat(os.path.join(ROOT, rel)).st_size
    except OSError:
        return None


def emit(op, rel, ts, frm=None, is_dir=False):
    global emitted, dropped
    if emitted >= MAX_EVENTS:
        dropped += 1
        return
    rec = {"t": "fs", "ts": ts, "op": op, "path": rel}
    if frm is not None:
        rec["from"] = frm
    if is_dir:
        rec["dir"] = True
    elif op != "delete":
        s = size(rel)
        if s is not None:
            rec["size"] = s
    out.write(json.dumps(rec, separators=(",", ":")) + "\n")
    emitted += 1


def watch(rel):
    if rel in wds or skipped(rel):
        return False
    if len(wds) >= MAX_WATCHES:
        if not notes or not notes[-1].startswith("watch limit"):
            notes.append("watch limit of %d folders reached; deeper folders unwatched" % MAX_WATCHES)
        return False
    wd = libc.inotify_add_watch(fd, os.path.join(ROOT, rel).encode(), MASK)
    if wd < 0:
        return False
    paths[wd], wds[rel] = rel, wd
    return True


def walk(rel, announce, ts):
    # Watches rel and every folder under it; with announce, files found are
    # reported as created (they appeared before the watch existed).
    if not watch(rel):
        return
    try:
        entries = list(os.scandir(os.path.join(ROOT, rel)))
    except OSError:
        return
    for e in entries:
        child = e.name if rel == "" else rel + "/" + e.name
        if skipped(child):
            continue
        is_dir = e.is_dir(follow_symlinks=False)
        if announce:
            emit("create", child, ts, is_dir=is_dir)
        if is_dir:
            walk(child, announce, ts)


def forget(rel):
    for p in [p for p in wds if p == rel or p.startswith(rel + "/")]:
        paths.pop(wds.pop(p), None)


def flush_pending(now, force):
    for rel, (op, first) in list(pending.items()):
        if force or now - first >= 2.0:
            emit(op, rel, now)
            del pending[rel]


def on_term(_sig, _frm):
    global stopping
    stopping = True


signal.signal(signal.SIGTERM, on_term)
signal.signal(signal.SIGINT, on_term)
if fd < 0:
    out.write(json.dumps({"t": "error", "error": "inotify_init1 failed: errno %d" % ctypes.get_errno()}, separators=(",", ":")) + "\n")
    sys.exit(1)
walk("", False, time.time())
out.write(json.dumps({"t": "ready", "watches": len(wds)}, separators=(",", ":")) + "\n")
out.flush()
moves = {}  # cookie -> (relative path, is_dir, time)
while not stopping:
    try:
        r, _, _ = select.select([fd], [], [], 0.25)
    except InterruptedError:
        r = []
    now = time.time()
    buf = b""
    if r:
        try:
            buf = os.read(fd, 1 << 20)
        except BlockingIOError:
            buf = b""
    i = 0
    while i + 16 <= len(buf):
        wd, mask, cookie, ln = struct.unpack_from("iIII", buf, i)
        name = buf[i + 16:i + 16 + ln].split(b"\0", 1)[0].decode("utf-8", "replace")
        i += 16 + ln
        if mask & IN_Q_OVERFLOW:
            notes.append("kernel event queue overflowed at %.3f; some changes were lost" % now)
            continue
        if mask & IN_IGNORED:
            rel = paths.pop(wd, None)
            if rel is not None:
                wds.pop(rel, None)
            continue
        base = paths.get(wd)
        if base is None or not name:
            continue
        rel = name if base == "" else base + "/" + name
        if skipped(rel):
            continue
        is_dir = bool(mask & IN_ISDIR)
        if mask & IN_CREATE:
            if is_dir:
                emit("create", rel, now, is_dir=True)
                walk(rel, True, now)
            else:
                pending[rel] = ["create", now]
        if mask & IN_MODIFY and not is_dir and rel not in pending:
            pending[rel] = ["modify", now]
        if mask & IN_CLOSE_WRITE and rel in pending:
            emit(pending.pop(rel)[0], rel, now)
        if mask & IN_DELETE:
            if rel in pending:
                emit(pending.pop(rel)[0], rel, now)
            emit("delete", rel, now, is_dir=is_dir)
            if is_dir:
                forget(rel)
        if mask & IN_MOVED_FROM:
            if rel in pending:
                emit(pending.pop(rel)[0], rel, now)
            moves[cookie] = (rel, is_dir, now)
        if mask & IN_MOVED_TO:
            src = moves.pop(cookie, None)
            if src is None:
                emit("create", rel, now, is_dir=is_dir)
                if is_dir:
                    walk(rel, True, now)
            else:
                emit("rename", rel, now, frm=src[0], is_dir=is_dir)
                if is_dir:
                    forget(src[0])
                    walk(rel, False, now)
    for cookie, (rel, is_dir, at) in list(moves.items()):
        if now - at > 0.5:
            emit("delete", rel, at, is_dir=is_dir)
            if is_dir:
                forget(rel)
            del moves[cookie]
    flush_pending(now, False)
    out.flush()
now = time.time()
for cookie, (rel, is_dir, at) in moves.items():
    emit("delete", rel, at, is_dir=is_dir)
flush_pending(now, True)
out.write(json.dumps({"t": "stats", "events": emitted, "dropped": dropped, "watches": len(wds), "notes": notes, "final": True}, separators=(",", ":")) + "\n")
out.flush()
