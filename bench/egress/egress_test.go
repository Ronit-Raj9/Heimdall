package egress

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type world struct {
	t        *testing.T
	ca       *CA
	tlsUp    *httptest.Server
	plainUp  *httptest.Server
	upstream http.RoundTripper
	hits     map[string]int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pemBytes, err := NewCA("test")
	if err != nil {
		t.Fatal(err)
	}
	ca, err := LoadCA(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, ca: ca, hits: map[string]int{}}
	big := bytes.Repeat([]byte("0123456789abcdef"), 70000) // over a MiB, many tar blocks
	h := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.hits[r.Method+" "+r.URL.Path]++
		switch r.URL.Path {
		case "/simple/numpy/":
			rw.Header().Set("Content-Type", "text/html")
			rw.Header().Set("X-Upstream", "1")
			io.WriteString(rw, "<a href='numpy-2.whl'>numpy</a>")
		case "/numpy-2.whl":
			rw.Write(big)
		case "/upload-pack":
			b, _ := io.ReadAll(r.Body)
			io.WriteString(rw, "pack for "+string(b))
		default:
			http.NotFound(rw, r)
		}
	})
	w.tlsUp = httptest.NewTLSServer(h)
	w.plainUp = httptest.NewServer(h)
	t.Cleanup(w.tlsUp.Close)
	t.Cleanup(w.plainUp.Close)
	up := NewTransport()
	up.TLSClientConfig = w.tlsUp.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	w.upstream = up
	return w
}

func (w *world) client(proxy *httptest.Server) *http.Client {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(w.ca.CertPEM())
	pu, _ := url.Parse(proxy.URL)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: &tls.Config{RootCAs: pool}, DisableCompression: true}}
}

type got struct {
	status int
	body   string
	header http.Header
}

func do(t *testing.T, c *http.Client, method, u, body string) got {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, u, rd)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return got{resp.StatusCode, string(b), resp.Header}
}

func TestRecordThenReplayByteForByte(t *testing.T) {
	w := newWorld(t)
	dir := t.TempDir()
	rec, err := NewRecorder(filepath.Join(dir, "rec"), 0)
	if err != nil {
		t.Fatal(err)
	}
	var recLog bytes.Buffer
	rs := &Server{Mode: ModeRecord, CA: w.ca, Recorder: rec, Transport: w.upstream, Log: &recLog}
	rp := httptest.NewServer(rs)
	defer rp.Close()
	c := w.client(rp)
	reqs := []struct{ method, url, body string }{
		{"GET", w.tlsUp.URL + "/simple/numpy/", ""},
		{"GET", w.tlsUp.URL + "/numpy-2.whl", ""},
		{"POST", w.tlsUp.URL + "/upload-pack", "want abc"},
		{"GET", w.plainUp.URL + "/simple/numpy/", ""},
		{"GET", w.tlsUp.URL + "/missing", ""},
	}
	var live []got
	for _, r := range reqs {
		live = append(live, do(t, c, r.method, r.url, r.body))
	}
	if live[0].status != 200 || !strings.Contains(live[0].body, "numpy") || live[4].status != 404 {
		t.Fatalf("live answers wrong: %+v", live)
	}
	rec.Close()
	if n := rs.Counts()[OutcomeRecorded]; n != 5 {
		t.Fatalf("recorded %d, want 5; log:\n%s", n, recLog.String())
	}
	cachePath := filepath.Join(dir, "cache.tar")
	f, _ := os.Create(cachePath)
	rows, err := Pack(filepath.Join(dir, "rec"), f)
	f.Close()
	if err != nil || len(rows) != 5 {
		t.Fatalf("pack: %d rows, %v", len(rows), err)
	}
	// Packing again gives the same bytes.
	var again bytes.Buffer
	if _, err := Pack(filepath.Join(dir, "rec"), &again); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(cachePath)
	if !bytes.Equal(first, again.Bytes()) {
		t.Fatal("pack is not deterministic")
	}
	cache, err := OpenCache(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	hitsBefore := len(w.hits)
	countBefore := 0
	for _, n := range w.hits {
		countBefore += n
	}
	var log bytes.Buffer
	ps := &Server{Mode: ModeReplay, CA: w.ca, Cache: cache, Log: &log}
	pp := httptest.NewServer(ps)
	defer pp.Close()
	c2 := w.client(pp)
	for i, r := range reqs {
		g := do(t, c2, r.method, r.url, r.body)
		if g.status != live[i].status || g.body != live[i].body {
			t.Fatalf("replay %s %s: %d %q, want %d (%d bytes)", r.method, r.url, g.status, short(g.body), live[i].status, len(live[i].body))
		}
		if i == 0 && g.header.Get("X-Upstream") != "1" {
			t.Fatalf("recorded header lost: %v", g.header)
		}
	}
	// A POST with another body still replays (same method and URL).
	if g := do(t, c2, "POST", w.tlsUp.URL+"/upload-pack", "want other"); g.status != 200 || g.body != "pack for want abc" {
		t.Fatalf("loose POST replay: %d %q", g.status, g.body)
	}
	count := 0
	for _, n := range w.hits {
		count += n
	}
	if len(w.hits) != hitsBefore || count != countBefore {
		t.Fatal("replay reached the upstream")
	}
	// A request the recording lacks is a 404 and a miss row.
	if g := do(t, c2, "GET", w.tlsUp.URL+"/simple/scipy/", ""); g.status != 404 || g.header.Get("X-Heimdall-Egress") != OutcomeMiss {
		t.Fatalf("miss: %d %v", g.status, g.header)
	}
	if ps.Counts()[OutcomeMiss] != 1 || !strings.Contains(log.String(), `"outcome":"miss"`) {
		t.Fatalf("miss not logged: %v\n%s", ps.Counts(), log.String())
	}
}

func TestOfflineAndLiveAllowlist(t *testing.T) {
	w := newWorld(t)
	off := httptest.NewServer(&Server{Mode: ModeOffline, CA: w.ca})
	defer off.Close()
	if g := do(t, w.client(off), "GET", w.tlsUp.URL+"/simple/numpy/", ""); g.status != 403 {
		t.Fatalf("offline https: %d", g.status)
	}
	if g := do(t, w.client(off), "GET", w.plainUp.URL+"/simple/numpy/", ""); g.status != 403 {
		t.Fatalf("offline http: %d", g.status)
	}
	live := &Server{Mode: ModeLive, CA: w.ca, Allow: []string{"127.0.0.1"}, Transport: w.upstream}
	lp := httptest.NewServer(live)
	defer lp.Close()
	if g := do(t, w.client(lp), "GET", w.tlsUp.URL+"/simple/numpy/", ""); g.status != 200 {
		t.Fatalf("live allowed: %d %s", g.status, g.body)
	}
	live.Allow = []string{"pypi.org"}
	if g := do(t, w.client(lp), "GET", w.tlsUp.URL+"/simple/numpy/", ""); g.status != 403 {
		t.Fatalf("live refused: %d", g.status)
	}
}

func TestAllowedAndNormalize(t *testing.T) {
	cases := []struct {
		list []string
		host string
		want bool
	}{
		{[]string{"pypi.org"}, "pypi.org", true},
		{[]string{"pypi.org"}, "files.pypi.org", false},
		{[]string{"*.pythonhosted.org"}, "files.pythonhosted.org", true},
		{[]string{".github.com"}, "github.com", true},
		{[]string{"*"}, "anything.example", true},
		{nil, "pypi.org", false},
	}
	for _, c := range cases {
		if got := Allowed(c.list, c.host); got != c.want {
			t.Errorf("Allowed(%v, %s) = %v", c.list, c.host, got)
		}
	}
	if got := NormalizeURL("HTTPS://PyPI.org:443/simple/#x"); got != "https://pypi.org/simple/" {
		t.Errorf("normalize: %s", got)
	}
	if got := NormalizeURL("http://h:8080/a?b=1"); got != "http://h:8080/a?b=1" {
		t.Errorf("normalize port: %s", got)
	}
}

func TestClientEnvAndHosts(t *testing.T) {
	env := ClientEnv(ProxyURL(ServiceID), []string{"bench-gateway", "egress"})
	if env["HTTPS_PROXY"] != "http://egress:3128" || !strings.Contains(env["NO_PROXY"], "bench-gateway") || env["SSL_CERT_FILE"] != BundlePath {
		t.Fatalf("env: %v", env)
	}
	hosts := HostsOf(map[string]string{"OPENAI_BASE_URL": "http://bench-gateway:8080/v1", "X": "plain"})
	if len(hosts) != 1 || hosts[0] != "bench-gateway" {
		t.Fatalf("hosts: %v", hosts)
	}
}

func TestCertOnlyDropsKey(t *testing.T) {
	p, _ := NewCA("x")
	c := CertOnly(p)
	if strings.Contains(string(c), "PRIVATE KEY") || !strings.Contains(string(c), "CERTIFICATE") {
		t.Fatal("CertOnly kept the key or lost the certificate")
	}
	if _, err := LoadCA(c); err == nil {
		t.Fatal("a certificate without key loaded as a CA")
	}
}

func TestEmptyCacheOpens(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.tar")
	os.WriteFile(p, EmptyCache(), 0o644)
	c, err := OpenCache(p)
	if err != nil || len(c.Entries()) != 0 {
		t.Fatalf("empty cache: %v", err)
	}
	c.Close()
}

func short(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func TestRecordRewriteKeepsAskedURL(t *testing.T) {
	w := newWorld(t)
	dir := t.TempDir()
	rec, err := NewRecorder(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	rws, err := ParseRewrites([]string{"https://archive.example/=" + w.tlsUp.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	rp := httptest.NewServer(&Server{Mode: ModeRecord, CA: w.ca, Recorder: rec, Transport: w.upstream, Rewrite: rws})
	defer rp.Close()
	if g := do(t, w.client(rp), "GET", "https://archive.example/simple/numpy/", ""); g.status != 200 || !strings.Contains(g.body, "numpy") {
		t.Fatalf("rewritten fetch: %d %q", g.status, g.body)
	}
	rec.Close()
	rows, _ := ReadManifest(filepath.Join(dir, "manifest.jsonl"))
	if len(rows) != 1 || rows[0].URL != "https://archive.example/simple/numpy/" {
		t.Fatalf("rows = %+v", rows)
	}
	if _, err := ParseRewrites([]string{"nope"}); err == nil {
		t.Fatal("bad rewrite accepted")
	}
}
