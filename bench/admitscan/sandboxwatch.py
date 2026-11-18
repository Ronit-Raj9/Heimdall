"""Admission sandbox watcher for `bifrost-bench skill scan -dynamic` and
`bifrost-bench mcp scan`.

Runs as root with only CAP_NET_RAW and CAP_DAC_READ_SEARCH, in the network
namespace the subject joins, with the subject's home volume mounted read-only
at /w. It never sends a packet and never opens a canary file. It prints a
ready line, then one JSON line per distinct event, then a stats line when it
gets SIGTERM:

- open, access: a canary file was opened or read (inotify IN_OPEN and
  IN_ACCESS on the file's inode, which is shared with the subject's mount of
  the same volume);
- dir_open: a credential folder (~/.ssh, ~/.aws, ~/.config/gh) was opened as
  a folder, which is what listing it does;
- dns: a DNS query the subject sent (loopback datagrams to Docker's resolver,
  or any datagram to port 53);
- syn, udp: a TCP connection attempt or a non-DNS datagram leaving on a
  non-loopback interface;
- arp: an ARP request, which is how a connection to any outside address shows
  up (the sandbox has only an on-link default route on an internal network,
  so nothing answers and the connect fails). resolver is true when the
  address is one of Docker's upstream DNS servers, which Docker's resolver
  contacts on the subject's behalf when the subject looks up a name.

argv[1] is a JSON object: {"root": "/w", "home": "/home/agent",
"files": ["<relative canary path>", ...], "dirs": ["<relative folder>", ...],
"max": 2000}. Standard library only; a malformed packet never stops it.
"""
import ctypes
import json
import os
import re
import select
import signal
import socket
import struct
import sys

ETH_P_ALL = 0x0003
PACKET_OUTGOING = 4
IN_ACCESS, IN_OPEN, IN_ISDIR, IN_IGNORED, IN_Q_OVERFLOW = 0x1, 0x20, 0x40000000, 0x8000, 0x4000
IN_ONLYDIR, IN_DONT_FOLLOW = 0x01000000, 0x02000000

cfg = json.loads(sys.argv[1])
ROOT = cfg.get("root", "/w")
HOME = cfg.get("home", "/home/agent")
MAX = int(cfg.get("max", 2000))
out = sys.stdout
seen = set()
emitted = 0
dropped = 0
notes = []
stopping = False


def emit(rec):
    global emitted, dropped
    key = json.dumps(rec, sort_keys=True, separators=(",", ":"))
    if key in seen:
        return
    if emitted >= MAX:
        dropped += 1
        return
    seen.add(key)
    emitted += 1
    out.write(key + "\n")
    out.flush()


def resolvers():
    # Docker writes the host's upstream servers as a comment:
    # "# ExtServers: [172.16.1.2 host(10.0.0.2) 8.8.8.8]".
    found = set()
    try:
        with open("/etc/resolv.conf") as f:
            for line in f:
                if line.startswith("# ExtServers:"):
                    found.update(re.findall(r"\d+\.\d+\.\d+\.\d+", line))
    except OSError:
        pass
    return found


RESOLVERS = resolvers()


def dns_name(b, i, depth=0):
    labels = []
    while i < len(b) and depth < 20:
        n = b[i]
        if n == 0:
            return ".".join(labels), i + 1
        if n & 0xC0 == 0xC0:
            if i + 1 >= len(b):
                break
            name, _ = dns_name(b, ((n & 0x3F) << 8) | b[i + 1], depth + 1)
            labels.append(name)
            return ".".join(x for x in labels if x), i + 2
        labels.append(b[i + 1:i + 1 + n].decode("ascii", "replace"))
        i += 1 + n
    return ".".join(labels), i


def dns_query(payload):
    # Only queries: answers come from the resolver, not from the subject.
    if len(payload) < 12:
        return False
    flags, qd = struct.unpack("!HH", payload[2:6])
    if flags & 0x8000 or qd < 1:
        return False
    name, i = dns_name(payload, 12)
    if not name:
        return False
    qtype = struct.unpack("!H", payload[i:i + 2])[0] if i + 2 <= len(payload) else 0
    emit({"t": "dns", "name": name.lower().rstrip("."), "qtype": qtype})
    return True


def ipv4(data, ifname):
    ip = data[14:]
    if len(ip) < 20:
        return
    ihl = (ip[0] & 0x0F) * 4
    proto = ip[9]
    dst = socket.inet_ntoa(ip[16:20])
    l4 = ip[ihl:]
    if len(l4) < 8:
        return
    sport, dport = struct.unpack("!HH", l4[:4])
    loop = ifname == "lo" or dst.startswith("127.")
    if proto == 17:
        payload = l4[8:]
        # Docker's resolver listens on 127.0.0.11 behind a NAT to a random
        # port, so loopback datagrams are read as DNS; other loopback
        # traffic is local and ignored.
        if loop:
            dns_query(payload)
        elif dport == 53:
            if not dns_query(payload):
                emit({"t": "udp", "dst": dst, "port": dport})
        else:
            emit({"t": "udp", "dst": dst, "port": dport})
    elif proto == 6 and len(l4) >= 20 and not loop:
        flags = l4[13]
        if flags & 0x02 and not flags & 0x10:
            emit({"t": "syn", "dst": dst, "port": dport})


def arp(data):
    a = data[14:]
    if len(a) < 28:
        return
    op = struct.unpack("!H", a[6:8])[0]
    if op != 1:
        return
    src = socket.inet_ntoa(a[14:18])
    dst = socket.inet_ntoa(a[24:28])
    # Docker announces the address it gave the namespace with gratuitous
    # ARP (sender and target the same) and the kernel may probe with sender
    # 0.0.0.0; neither is the subject asking for a host.
    if src == dst or src == "0.0.0.0":
        return
    rec = {"t": "arp", "dst": dst}
    if dst in RESOLVERS:
        rec["resolver"] = True
    emit(rec)


def packet(data, addr):
    if addr[2] != PACKET_OUTGOING or len(data) < 14:
        return
    if addr[1] == 0x0800:
        ipv4(data, addr[0])
    elif addr[1] == 0x0806:
        arp(data)


libc = ctypes.CDLL(None, use_errno=True)
watches = {}  # wd -> (container path, is folder)


def add_watch(rel, is_dir):
    mask = (IN_OPEN | IN_ONLYDIR) if is_dir else (IN_OPEN | IN_ACCESS)
    wd = libc.inotify_add_watch(ifd, os.path.join(ROOT, rel).encode(), mask | IN_DONT_FOLLOW)
    if wd < 0:
        notes.append("watch %s: errno %d" % (rel, ctypes.get_errno()))
        return False
    watches[wd] = (HOME + "/" + rel, is_dir)
    return True


def inotify_events(buf):
    i = 0
    while i + 16 <= len(buf):
        wd, mask, _, ln = struct.unpack_from("iIII", buf, i)
        name = buf[i + 16:i + 16 + ln].split(b"\0", 1)[0]
        i += 16 + ln
        if mask & IN_Q_OVERFLOW:
            notes.append("inotify queue overflowed; some opens were lost")
            continue
        if mask & IN_IGNORED or wd not in watches:
            continue
        path, is_dir = watches[wd]
        if is_dir:
            # Only the folder itself being opened (a listing); opens of
            # files inside it are reported by the files' own watches.
            if not name and mask & IN_OPEN:
                emit({"t": "dir_open", "path": path})
            continue
        if mask & IN_OPEN:
            emit({"t": "open", "path": path})
        if mask & IN_ACCESS:
            emit({"t": "access", "path": path})


def on_term(_sig, _frm):
    global stopping
    stopping = True


signal.signal(signal.SIGTERM, on_term)
signal.signal(signal.SIGINT, on_term)
ifd = libc.inotify_init1(os.O_NONBLOCK | os.O_CLOEXEC)
if ifd < 0:
    out.write(json.dumps({"t": "error", "error": "inotify_init1 failed: errno %d" % ctypes.get_errno()}) + "\n")
    out.flush()
    sys.exit(1)
files = [f for f in cfg.get("files", []) if add_watch(f, False)]
dirs = [d for d in cfg.get("dirs", []) if add_watch(d, True)]
try:
    sock = socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.ntohs(ETH_P_ALL))
    sock.setblocking(False)
except OSError as e:
    out.write(json.dumps({"t": "error", "error": "packet socket: %s" % e}) + "\n")
    out.flush()
    sys.exit(1)
out.write(json.dumps({"t": "ready", "files": len(files), "dirs": len(dirs), "resolvers": sorted(RESOLVERS), "notes": notes}) + "\n")
out.flush()
while not stopping:
    try:
        r, _, _ = select.select([ifd, sock], [], [], 0.25)
    except InterruptedError:
        r = []
    if ifd in r:
        try:
            inotify_events(os.read(ifd, 1 << 16))
        except BlockingIOError:
            pass
    if sock in r:
        for _ in range(256):
            try:
                data, addr = sock.recvfrom(65535)
            except (BlockingIOError, InterruptedError):
                break
            try:
                packet(data, addr)
            except Exception as e:  # a malformed packet must never stop the watcher
                notes.append("packet: %s" % type(e).__name__)
# Drain what arrived before the signal.
try:
    inotify_events(os.read(ifd, 1 << 16))
except BlockingIOError:
    pass
out.write(json.dumps({"t": "stats", "events": emitted, "dropped": dropped, "notes": notes[:20]}) + "\n")
out.flush()
