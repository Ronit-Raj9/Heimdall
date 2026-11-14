package environments

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadNetWatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), NetWatchFile)
	content := `{"t":"dns","name":"example.com","qtype":1}
not json
{"t":"http","dst":"172.20.0.3","port":8080,"method":"POST","path":"/openai/v1/chat/completions","host":"bifrost:8080","headers":["authorization","x-bf-session-id"],"session":"r_1"}

`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadNetWatch(p)
	if err != nil || len(recs) != 2 {
		t.Fatalf("records %+v err %v", recs, err)
	}
	if recs[0].T != NetWatchDNS || recs[0].Name != "example.com" || recs[1].Session != "r_1" || recs[1].Port != 8080 {
		t.Fatalf("records = %+v", recs)
	}
	if _, err := ReadNetWatch(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing file read")
	}
	dir := t.TempDir()
	if err := writeNetWatchError(dir, os.ErrPermission); err != nil {
		t.Fatal(err)
	}
	recs, err = ReadNetWatch(filepath.Join(dir, NetWatchFile))
	if err != nil || len(recs) != 1 || recs[0].T != NetWatchError || recs[0].Error == "" {
		t.Fatalf("error record %+v %v", recs, err)
	}
}

// netwatchHarness feeds hand-built packets to netwatch.py's packet() and
// prints what it emits.
const netwatchHarness = `import socket, struct, sys
sys.path.insert(0, sys.argv[1])
import netwatch

OUT = 4
def ip(proto, dst, payload, src="172.20.0.5"):
    hdr = struct.pack("!BBHHHBBH4s4s", 0x45, 0, 20 + len(payload), 0, 0, 64, proto, 0, socket.inet_aton(src), socket.inet_aton(dst))
    return b"\0" * 14 + hdr + payload
def udp(sport, dport, data):
    return struct.pack("!HHHH", sport, dport, 8 + len(data), 0) + data
def tcp(dport, flags, data=b""):
    return struct.pack("!HHIIBBHHH", 40000, dport, 0, 0, 5 << 4, flags, 0, 0, 0) + data
def qname(name):
    return b"".join(bytes([len(p)]) + p.encode() for p in name.split(".")) + b"\0"
def dnsq(name):
    return struct.pack("!HHHHHH", 1, 0x0100, 1, 0, 0, 0) + qname(name) + struct.pack("!HH", 1, 1)
def dnsa(name, addr):
    return (struct.pack("!HHHHHH", 1, 0x8180, 1, 1, 0, 0) + qname(name) + struct.pack("!HH", 1, 1)
            + struct.pack("!HHHIH", 0xC00C, 1, 1, 60, 4) + socket.inet_aton(addr))
lo, eth = ("lo", 0x0800, OUT, 772, b""), ("eth0", 0x0800, OUT, 1, b"")
http = (b"POST /openai/v1/chat/completions?beta=1 HTTP/1.1\r\nHost: bifrost:8080\r\n"
        b"Authorization: Bearer sk-bf-secret\r\nX-Bf-Session-Id: r_1\r\n\r\n{}")
netwatch.packet(ip(17, "127.0.0.11", udp(40000, 34567, dnsq("Example.COM"))), lo)
netwatch.packet(ip(17, "127.0.0.1", udp(34567, 40000, dnsa("bifrost", "172.20.0.3")), src="127.0.0.11"), lo)
netwatch.packet(ip(6, "172.20.0.3", tcp(8080, 0x02)), eth)
netwatch.packet(ip(6, "172.20.0.3", tcp(8080, 0x02)), eth)
netwatch.packet(ip(6, "172.20.0.3", tcp(8080, 0x18, http)), eth)
netwatch.packet(ip(17, "10.9.9.9", udp(5000, 9999, b"x")), eth)
netwatch.packet(ip(6, "1.2.3.4", tcp(443, 0x02)), ("eth0", 0x0800, 0, 1, b""))
netwatch.packet(b"short", eth)
netwatch.dns(b"\0" * 12 + b"\x05ab")
netwatch.dns(struct.pack("!HHHHHH", 1, 0x8180, 1, 5, 0, 0) + qname("x.y") + b"\0\1\0\1" + b"\xc0")
`

func TestNetWatchScript(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "netwatch.py"), []byte(netwatchScript), 0o644); err != nil {
		t.Fatal(err)
	}
	h := filepath.Join(t.TempDir(), "harness.py")
	if err := os.WriteFile(h, []byte(netwatchHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(py, "-I", "-B", h, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "sk-bf-secret") {
		t.Fatalf("observer printed a header value:\n%s", out)
	}
	p := filepath.Join(t.TempDir(), NetWatchFile)
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadNetWatch(p)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, r := range recs {
		switch {
		case r.T == NetWatchDNS && r.Name == "example.com":
			got["dns"]++
		case r.T == NetWatchDNSAnswer && r.Name == "bifrost" && len(r.IPs) == 1 && r.IPs[0] == "172.20.0.3":
			got["answer"]++
		case r.T == NetWatchSYN && r.Dst == "172.20.0.3" && r.Port == 8080:
			got["syn"]++
		case r.T == NetWatchHTTP && r.Path == "/openai/v1/chat/completions" && r.Host == "bifrost:8080" && r.Session == "r_1" &&
			strings.Join(r.Headers, ",") == "authorization,host,x-bf-session-id":
			got["http"]++
		case r.T == NetWatchUDP && r.Dst == "10.9.9.9" && r.Port == 9999:
			got["udp"]++
		default:
			t.Errorf("unexpected record %+v", r)
		}
	}
	for _, k := range []string{"dns", "answer", "syn", "http", "udp"} {
		if got[k] != 1 {
			t.Errorf("%s records = %d, want 1 (duplicates are dropped, incoming packets ignored)\n%s", k, got[k], out)
		}
	}
}
