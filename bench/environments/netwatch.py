"""Passive network observer for `bifrost-bench agent check`.

Runs as root with only CAP_NET_RAW in the network namespace the agent
container joins, and prints one JSON line per distinct thing it sees leave
the agent: DNS queries (and the A records answered), TCP connection attempts,
other UDP datagrams, and the request line and header names of plain HTTP
requests (header values are never printed, except x-bf-session-id). It never
sends a packet. Standard library only; it must never crash on a packet.
"""
import json
import signal
import socket
import struct
import sys

ETH_P_ALL = 0x0003
PACKET_OUTGOING = 4
MAX_LINES = 5000
SESSION_HEADER = "x-bf-session-id"
METHODS = (b"GET ", b"POST ", b"PUT ", b"PATCH ", b"DELETE ", b"HEAD ", b"OPTIONS ")

seen = set()
lines = 0


def emit(rec):
    global lines
    key = json.dumps(rec, sort_keys=True)
    if key in seen or lines >= MAX_LINES:
        return
    seen.add(key)
    lines += 1
    sys.stdout.write(key + "\n")
    sys.stdout.flush()


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


def dns(payload):
    if len(payload) < 12:
        return
    flags, qd, an = struct.unpack("!HHH", payload[2:8])
    name, i = dns_name(payload, 12)
    if not name or qd < 1:
        return
    qtype = struct.unpack("!H", payload[i:i + 2])[0] if i + 2 <= len(payload) else 0
    if not flags & 0x8000:
        emit({"t": "dns", "name": name.lower(), "qtype": qtype})
        return
    i += 4
    ips = []
    for _ in range(an):
        _, i = dns_name(payload, i)
        if i + 10 > len(payload):
            break
        rtype, _, _, rdlen = struct.unpack("!HHIH", payload[i:i + 10])
        i += 10
        if rtype == 1 and rdlen == 4:
            ips.append(socket.inet_ntoa(payload[i:i + 4]))
        i += rdlen
    if ips:
        emit({"t": "dns_answer", "name": name.lower(), "ips": sorted(ips)})


def http(dst, port, payload):
    if not payload.startswith(METHODS):
        return
    head = payload.split(b"\r\n\r\n", 1)[0].decode("latin-1").split("\r\n")
    parts = head[0].split(" ")
    if len(parts) < 2:
        return
    rec = {"t": "http", "dst": dst, "port": port, "method": parts[0], "path": parts[1].split("?", 1)[0], "headers": []}
    for h in head[1:]:
        name, _, value = h.partition(":")
        name = name.strip().lower()
        if not name:
            continue
        rec["headers"].append(name)
        if name == "host":
            rec["host"] = value.strip()
        elif name == SESSION_HEADER:
            rec["session"] = value.strip()
    rec["headers"] = sorted(set(rec["headers"]))
    emit(rec)


def packet(data, addr):
    if addr[2] != PACKET_OUTGOING or addr[1] != 0x0800 or len(data) < 34:
        return
    ip = data[14:]
    ihl = (ip[0] & 0x0F) * 4
    proto = ip[9]
    dst = socket.inet_ntoa(ip[16:20])
    l4 = ip[ihl:]
    if len(l4) < 8:
        return
    sport, dport = struct.unpack("!HH", l4[:4])
    if proto == 17:
        payload = l4[8:]
        # Docker's resolver listens on 127.0.0.11 behind a NAT to a random
        # port, so every loopback datagram is read as DNS; anything else that
        # leaves on UDP is reported as is.
        if dport == 53 or sport == 53 or dst.startswith("127."):
            dns(payload)
        else:
            emit({"t": "udp", "dst": dst, "port": dport})
    elif proto == 6 and len(l4) >= 20:
        flags = l4[13]
        if flags & 0x02 and not flags & 0x10:
            emit({"t": "syn", "dst": dst, "port": dport})
        http(dst, dport, l4[(l4[12] >> 4) * 4:])


def main():
    s = socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.ntohs(ETH_P_ALL))
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    sys.stdout.write(json.dumps({"t": "ready"}) + "\n")
    sys.stdout.flush()
    while True:
        data, addr = s.recvfrom(65535)
        try:
            packet(data, addr)
        except Exception as e:  # a malformed packet must never stop the observer
            emit({"t": "error", "error": type(e).__name__})


if __name__ == "__main__":
    main()
