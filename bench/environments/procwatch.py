# Process observer. Runs in a trusted helper container that shares the agent
# container's PID namespace (--pid=container:<agent>), with no network, a
# read-only root and only CAP_SYS_PTRACE (to read the working directory and
# the exit code of a zombie). It polls /proc and prints one JSON line per
# process start and end on stdout; the bench redacts and stores them.
#
# Linux has no unprivileged exec event stream, so this polls. A process that
# starts and ends between two polls is missed; the kernel's per-namespace
# last-PID counter (/proc/sys/kernel/ns_last_pid) says how many PIDs were
# handed out, so the miss count is bounded and reported, never hidden.
#
# argv[1] is the poll interval in milliseconds.
import json
import os
import signal
import sys
import time

INTERVAL = max(int(sys.argv[1]) if len(sys.argv) > 1 else 20, 1) / 1000.0
ARG_CAP = 2048
ARGS_MAX = 64
SELF = os.getpid()
CLK = os.sysconf("SC_CLK_TCK")
BOOT = time.time() - time.clock_gettime(time.CLOCK_BOOTTIME)
out = sys.stdout
stopping = False


def emit(rec):
    out.write(json.dumps(rec, separators=(",", ":")) + "\n")


def last_pid():
    try:
        with open("/proc/sys/kernel/ns_last_pid") as f:
            return int(f.read().strip())
    except (OSError, ValueError):
        return -1


def stat(pid):
    # Fields after the parenthesised comm, which may itself hold spaces.
    with open("/proc/%d/stat" % pid, "rb") as f:
        raw = f.read().decode("utf-8", "replace")
    lp, rp = raw.find("("), raw.rfind(")")
    comm = raw[lp + 1:rp]
    rest = raw[rp + 2:].split()
    return comm, rest


def argv(pid):
    try:
        with open("/proc/%d/cmdline" % pid, "rb") as f:
            raw = f.read(ARG_CAP * ARGS_MAX + 1)
    except OSError:
        return [], False
    cut = len(raw) > ARG_CAP * ARGS_MAX
    parts = raw.rstrip(b"\0").split(b"\0") if raw else []
    if len(parts) > ARGS_MAX:
        parts, cut = parts[:ARGS_MAX], True
    res = []
    for p in parts:
        if len(p) > ARG_CAP:
            p, cut = p[:ARG_CAP], True
        res.append(p.decode("utf-8", "replace"))
    return res, cut


def uid(pid):
    try:
        with open("/proc/%d/status" % pid) as f:
            for line in f:
                if line.startswith("Uid:"):
                    return int(line.split()[1])
    except (OSError, ValueError, IndexError):
        pass
    return -1


def cwd(pid):
    try:
        return os.readlink("/proc/%d/cwd" % pid)
    except OSError:
        return ""


live = {}  # pid -> start ticks
ended = set()  # (pid, start ticks) of zombies already reported
seen = set()  # every pid or tid seen
scans = 0


def on_term(_sig, _frm):
    global stopping
    stopping = True


signal.signal(signal.SIGTERM, on_term)
signal.signal(signal.SIGINT, on_term)


def scan(now):
    global scans
    scans += 1
    found = {}
    try:
        names = os.listdir("/proc")
    except OSError:
        return
    for name in names:
        if not name.isdigit():
            continue
        pid = int(name)
        if pid == SELF:
            seen.add(pid)
            continue
        try:
            comm, rest = stat(pid)
        except (OSError, IndexError):
            continue
        st = int(rest[19])
        if (pid, st) in ended:
            continue
        found[pid] = st
        seen.add(pid)
        try:
            for tid in os.listdir("/proc/%d/task" % pid):
                seen.add(int(tid))
        except (OSError, ValueError):
            pass
        state = rest[0]
        if live.get(pid) != st:
            if pid in live:
                emit({"t": "end", "pid": pid, "st": live[pid], "at": now})
            a, cut = argv(pid)
            rec = {"t": "start", "pid": pid, "ppid": int(rest[1]), "st": st,
                   "start": BOOT + st / CLK, "seen": now, "uid": uid(pid),
                   "comm": comm, "argv": a, "cwd": cwd(pid)}
            if cut:
                rec["cut"] = True
            emit(rec)
            live[pid] = st
        if state == "Z" and len(rest) > 49:
            code = int(rest[49])
            rec = {"t": "end", "pid": pid, "st": st, "at": now}
            if code & 0x7F == 0:
                rec["exit"] = (code >> 8) & 0xFF
            else:
                rec["signal"] = code & 0x7F
            emit(rec)
            live.pop(pid, None)
            found.pop(pid, None)
            ended.add((pid, st))
    for pid in list(live):
        if pid not in found:
            emit({"t": "end", "pid": pid, "st": live.pop(pid), "at": now})


def stats(final, lp=None):
    if lp is None:
        lp = last_pid()
    emit({"t": "stats", "alloc": lp, "seen": len([p for p in seen if p <= lp]) if lp > 0 else len(seen),
          "scans": scans, "cpu": round(time.process_time(), 3), "interval_ms": int(INTERVAL * 1000),
          "final": final})


emit({"t": "ready", "pid": SELF, "interval_ms": int(INTERVAL * 1000), "last_pid": last_pid()})
out.flush()
# A stats line goes out whenever the namespace handed out new PIDs (and at
# least once a second), because the observer usually dies with the agent's
# PID 1 and never prints a final one.
next_stats = time.time() + 1.0
reported = -1
while not stopping:
    now = time.time()
    scan(now)
    lp = last_pid()
    if lp != reported or now >= next_stats:
        stats(False, lp)
        reported, next_stats = lp, now + 1.0
    out.flush()
    time.sleep(INTERVAL)
scan(time.time())
stats(True)
out.flush()
