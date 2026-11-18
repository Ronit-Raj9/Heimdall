"""Adds an on-link default route (`default dev eth0`) in the sandbox network
namespace for `bifrost-bench skill scan -dynamic` and `mcp scan`.

The sandbox sits on an internal Docker network, which has no default route:
a connect to an outside address fails with ENETUNREACH before any packet
exists, so no observer could see the attempt. With an on-link default route
the kernel sends an ARP request for the outside address instead; nothing
answers it, the connect fails with EHOSTUNREACH after about three seconds,
and the watcher records the ARP request. Nothing leaves the internal network.

Runs once as root with only CAP_NET_ADMIN in the holder's namespace. Uses
rtnetlink directly, so the helper image needs python3 and nothing else.
Standard library only. argv[1] names the interface (default eth0).
"""
import os
import socket
import struct
import sys

RTM_NEWROUTE = 24
NLM_F_REQUEST, NLM_F_ACK, NLM_F_EXCL, NLM_F_CREATE = 0x1, 0x4, 0x200, 0x400
NLMSG_ERROR = 2
RT_TABLE_MAIN, RTPROT_STATIC, RT_SCOPE_LINK, RTN_UNICAST = 254, 4, 253, 1
RTA_OIF = 4


def main():
    dev = sys.argv[1] if len(sys.argv) > 1 else "eth0"
    try:
        ifindex = socket.if_nametoindex(dev)
    except OSError as e:
        print("route: no interface %s: %s" % (dev, e), file=sys.stderr)
        return 2
    attr = struct.pack("=HHI", 8, RTA_OIF, ifindex)
    rtmsg = struct.pack("=BBBBBBBBI", socket.AF_INET, 0, 0, 0, RT_TABLE_MAIN, RTPROT_STATIC, RT_SCOPE_LINK, RTN_UNICAST, 0)
    body = rtmsg + attr
    hdr = struct.pack("=IHHII", 16 + len(body), RTM_NEWROUTE, NLM_F_REQUEST | NLM_F_ACK | NLM_F_EXCL | NLM_F_CREATE, 1, 0)
    s = socket.socket(socket.AF_NETLINK, socket.SOCK_RAW, socket.NETLINK_ROUTE)
    s.bind((0, 0))
    s.send(hdr + body)
    resp = s.recv(65536)
    if len(resp) < 20:
        print("route: short netlink answer", file=sys.stderr)
        return 1
    _, mtype, _, _, _ = struct.unpack("=IHHII", resp[:16])
    if mtype != NLMSG_ERROR:
        print("route: unexpected netlink message type %d" % mtype, file=sys.stderr)
        return 1
    err = struct.unpack("=i", resp[16:20])[0]
    if err != 0:
        print("route: add default dev %s: %s" % (dev, os.strerror(-err)), file=sys.stderr)
        return 1
    print("ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
