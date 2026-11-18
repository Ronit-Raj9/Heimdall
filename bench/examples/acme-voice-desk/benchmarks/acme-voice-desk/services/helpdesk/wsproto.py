"""Minimal WebSocket (RFC 6455) for Heimdall's voice calls, standard library only.

Part of Heimdall (bench/voice). The voice caller, the scripted audio fakes,
the reference voice agents and the admission controls all speak WebSocket
without third-party packages, so every image stays small and every byte on
the wire is code this repository owns. It supports what a voice call needs:
text and binary messages, ping and pong, close, fragmented messages on read,
and masking on the client side. No extensions (no permessage-deflate) and no
subprotocol negotiation beyond echoing one the client offers.

Two halves:
  - asyncio server side: server_handshake(reader, writer) then AsyncConn;
  - blocking client side: connect(url, headers) returns a Conn whose recv()
    takes a timeout, used from plain threads.
"""

import asyncio
import base64
import hashlib
import os
import socket
import struct
import threading
import urllib.parse

GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
MAX_MESSAGE = 8 << 20
OP_CONT, OP_TEXT, OP_BINARY, OP_CLOSE, OP_PING, OP_PONG = 0x0, 0x1, 0x2, 0x8, 0x9, 0xA


class Closed(Exception):
    """The peer closed the connection (code and reason when it sent them)."""

    def __init__(self, code=1006, reason=""):
        super().__init__(f"websocket closed ({code}) {reason}".strip())
        self.code = code
        self.reason = reason


def accept_key(key):
    return base64.b64encode(hashlib.sha1((key + GUID).encode()).digest()).decode()


def encode_frame(op, payload, mask):
    head = bytearray([0x80 | op])
    n = len(payload)
    bit = 0x80 if mask else 0
    if n < 126:
        head.append(bit | n)
    elif n < 1 << 16:
        head.append(bit | 126)
        head += struct.pack("!H", n)
    else:
        head.append(bit | 127)
        head += struct.pack("!Q", n)
    if not mask:
        return bytes(head) + payload
    key = os.urandom(4)
    return bytes(head) + key + apply_mask(payload, key)


def apply_mask(payload, key):
    if not payload:
        return b""
    n = len(payload)
    k = (key * (n // 4 + 1))[:n]
    return (int.from_bytes(payload, "little") ^ int.from_bytes(k, "little")).to_bytes(n, "little")


def close_payload(code, reason):
    return struct.pack("!H", code) + reason.encode("utf-8")[:120]


def parse_close(payload):
    if len(payload) >= 2:
        return struct.unpack("!H", payload[:2])[0], payload[2:].decode("utf-8", "replace")
    return 1005, ""


# ---- asyncio server side ----

async def read_http_head(reader, limit=16384):
    raw = await reader.readuntil(b"\r\n\r\n")
    if len(raw) > limit:
        raise ValueError("request head too large")
    lines = raw.decode("latin-1").split("\r\n")
    method, path, _ = (lines[0].split(" ", 2) + ["", ""])[:3]
    headers = {}
    for line in lines[1:]:
        if ":" in line:
            k, v = line.split(":", 1)
            headers[k.strip().lower()] = v.strip()
    return method, path, headers


async def server_handshake(reader, writer, headers):
    """Answer an upgrade request whose head was already read; True on success."""
    key = headers.get("sec-websocket-key", "")
    if headers.get("upgrade", "").lower() != "websocket" or not key:
        writer.write(b"HTTP/1.1 426 Upgrade Required\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
        await writer.drain()
        return False
    out = ["HTTP/1.1 101 Switching Protocols", "Upgrade: websocket", "Connection: Upgrade",
           "Sec-WebSocket-Accept: " + accept_key(key)]
    proto = headers.get("sec-websocket-protocol", "")
    if proto:
        out.append("Sec-WebSocket-Protocol: " + proto.split(",")[0].strip())
    writer.write(("\r\n".join(out) + "\r\n\r\n").encode())
    await writer.drain()
    return True


class AsyncConn:
    """One server-side connection: recv() returns (kind, data) with kind text or binary."""

    def __init__(self, reader, writer, mask=False):
        self.reader, self.writer, self.mask = reader, writer, mask
        self.closed = False
        self.lock = asyncio.Lock()

    async def _send(self, op, payload):
        if self.closed and op != OP_CLOSE:
            raise Closed(1006, "send after close")
        async with self.lock:
            self.writer.write(encode_frame(op, payload, self.mask))
            await self.writer.drain()

    async def send_text(self, text):
        await self._send(OP_TEXT, text.encode("utf-8"))

    async def send_binary(self, data):
        await self._send(OP_BINARY, bytes(data))

    async def close(self, code=1000, reason=""):
        if self.closed:
            return
        try:
            await self._send(OP_CLOSE, close_payload(code, reason))
        except (OSError, ConnectionError, Closed):
            pass
        self.closed = True
        try:
            self.writer.close()
        except OSError:
            pass

    async def recv(self):
        parts, first_op = [], None
        while True:
            head = await self.reader.readexactly(2)
            fin, op = head[0] & 0x80, head[0] & 0x0F
            masked, n = head[1] & 0x80, head[1] & 0x7F
            if n == 126:
                n = struct.unpack("!H", await self.reader.readexactly(2))[0]
            elif n == 127:
                n = struct.unpack("!Q", await self.reader.readexactly(8))[0]
            if n > MAX_MESSAGE:
                await self.close(1009, "message too big")
                raise Closed(1009, "message too big")
            key = await self.reader.readexactly(4) if masked else None
            payload = await self.reader.readexactly(n) if n else b""
            if key:
                payload = apply_mask(payload, key)
            if op == OP_PING:
                await self._send(OP_PONG, payload)
                continue
            if op == OP_PONG:
                continue
            if op == OP_CLOSE:
                code, reason = parse_close(payload)
                await self.close(code if code not in (1005, 1006) else 1000)
                raise Closed(code, reason)
            if op != OP_CONT:
                first_op = op
            parts.append(payload)
            if sum(len(p) for p in parts) > MAX_MESSAGE:
                await self.close(1009, "message too big")
                raise Closed(1009, "message too big")
            if fin:
                data = b"".join(parts)
                if first_op == OP_TEXT:
                    return "text", data.decode("utf-8", "replace")
                return "binary", data


async def async_connect(url, headers=None):
    """asyncio client connect, for the realtime fake's tests and bridges."""
    u = urllib.parse.urlsplit(url)
    port = u.port or (443 if u.scheme == "wss" else 80)
    if u.scheme == "wss":
        raise ValueError("wss is not supported by this minimal client; use ws:// inside the run network")
    reader, writer = await asyncio.open_connection(u.hostname, port)
    key = base64.b64encode(os.urandom(16)).decode()
    path = (u.path or "/") + ("?" + u.query if u.query else "")
    lines = [f"GET {path} HTTP/1.1", f"Host: {u.hostname}:{port}", "Upgrade: websocket", "Connection: Upgrade",
             "Sec-WebSocket-Key: " + key, "Sec-WebSocket-Version: 13"]
    for k, v in (headers or {}).items():
        lines.append(f"{k}: {v}")
    writer.write(("\r\n".join(lines) + "\r\n\r\n").encode())
    await writer.drain()
    raw = await reader.readuntil(b"\r\n\r\n")
    status = raw.split(b"\r\n", 1)[0]
    if b" 101 " not in status + b" ":
        raise ConnectionError("websocket upgrade refused: " + status.decode("latin-1"))
    return AsyncConn(reader, writer, mask=True)


# ---- blocking client side ----

class Conn:
    """A blocking client connection. recv(timeout) returns (kind, data) or None on timeout."""

    def __init__(self, sock, rest=b"", mask=True):
        self.sock = sock
        self.buf = bytearray(rest)
        self.wlock = threading.Lock()
        self.closed = False
        self.mask = mask  # clients mask what they send, servers do not

    def _fill(self, n, timeout):
        self.sock.settimeout(timeout)
        while len(self.buf) < n:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise Closed(1006, "connection lost")
            self.buf += chunk

    def _take(self, n):
        out = bytes(self.buf[:n])
        del self.buf[:n]
        return out

    def _send(self, op, payload):
        with self.wlock:
            if self.closed and op != OP_CLOSE:
                raise Closed(1006, "send after close")
            self.sock.sendall(encode_frame(op, payload, self.mask))

    def send_text(self, text):
        self._send(OP_TEXT, text.encode("utf-8"))

    def send_binary(self, data):
        self._send(OP_BINARY, bytes(data))

    def close(self, code=1000, reason=""):
        if self.closed:
            return
        try:
            self._send(OP_CLOSE, close_payload(code, reason))
        except OSError:
            pass
        self.closed = True
        try:
            self.sock.close()
        except OSError:
            pass

    def recv(self, timeout=None):
        parts, first_op = [], None
        while True:
            try:
                self._fill(2, timeout)
            except socket.timeout:
                return None
            # A frame has started: read the rest of it without the short timeout.
            fin, op = self.buf[0] & 0x80, self.buf[0] & 0x0F
            n = self.buf[1] & 0x7F
            masked = self.buf[1] & 0x80
            hl = 2 + (2 if n == 126 else 8 if n == 127 else 0) + (4 if masked else 0)
            self._fill(hl, 30)
            if n == 126:
                n = struct.unpack("!H", bytes(self.buf[2:4]))[0]
            elif n == 127:
                n = struct.unpack("!Q", bytes(self.buf[2:10]))[0]
            if n > MAX_MESSAGE:
                self.close(1009, "message too big")
                raise Closed(1009, "message too big")
            self._fill(hl + n, 30)
            head = self._take(hl)
            payload = self._take(n)
            if masked:
                payload = apply_mask(payload, head[-4:])
            if op == OP_PING:
                self._send(OP_PONG, payload)
                continue
            if op == OP_PONG:
                continue
            if op == OP_CLOSE:
                code, reason = parse_close(payload)
                self.close()
                raise Closed(code, reason)
            if op != OP_CONT:
                first_op = op
            parts.append(payload)
            if fin:
                data = b"".join(parts)
                if first_op == OP_TEXT:
                    return "text", data.decode("utf-8", "replace")
                return "binary", data


def server_accept(sock, headers):
    """Blocking server-side upgrade for a threaded HTTP server; returns a Conn or None."""
    key = headers.get("sec-websocket-key", "")
    if headers.get("upgrade", "").lower() != "websocket" or not key:
        sock.sendall(b"HTTP/1.1 426 Upgrade Required\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
        return None
    out = ["HTTP/1.1 101 Switching Protocols", "Upgrade: websocket", "Connection: Upgrade",
           "Sec-WebSocket-Accept: " + accept_key(key)]
    proto = headers.get("sec-websocket-protocol", "")
    if proto:
        out.append("Sec-WebSocket-Protocol: " + proto.split(",")[0].strip())
    sock.sendall(("\r\n".join(out) + "\r\n\r\n").encode())
    return Conn(sock, b"", mask=False)


def connect(url, headers=None, timeout=10):
    """Open a blocking client connection to a ws:// URL."""
    u = urllib.parse.urlsplit(url)
    if u.scheme != "ws":
        raise ValueError(f"want a ws:// URL, got {url!r} (wss is not supported inside the run network)")
    port = u.port or 80
    sock = socket.create_connection((u.hostname, port), timeout=timeout)
    sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    key = base64.b64encode(os.urandom(16)).decode()
    path = (u.path or "/") + ("?" + u.query if u.query else "")
    lines = [f"GET {path} HTTP/1.1", f"Host: {u.hostname}:{port}", "Upgrade: websocket", "Connection: Upgrade",
             "Sec-WebSocket-Key: " + key, "Sec-WebSocket-Version: 13"]
    for k, v in (headers or {}).items():
        lines.append(f"{k}: {v}")
    sock.sendall(("\r\n".join(lines) + "\r\n\r\n").encode())
    raw = b""
    while b"\r\n\r\n" not in raw:
        chunk = sock.recv(4096)
        if not chunk:
            raise ConnectionError("websocket upgrade: connection closed")
        raw += chunk
        if len(raw) > 16384:
            raise ConnectionError("websocket upgrade: response head too large")
    head, rest = raw.split(b"\r\n\r\n", 1)
    status = head.split(b"\r\n", 1)[0].decode("latin-1")
    if " 101 " not in status + " ":
        raise ConnectionError("websocket upgrade refused: " + status)
    want = accept_key(key)
    if want.encode() not in head:
        raise ConnectionError("websocket upgrade: bad Sec-WebSocket-Accept")
    return Conn(sock, rest)
