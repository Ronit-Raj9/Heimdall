package k8s

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWSAcceptKnownVector(t *testing.T) {
	// RFC 6455 section 1.3.
	if got := wsAccept("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept = %q", got)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	for _, n := range []int{0, 5, 125, 126, 300, 65535, 65536, 70000} {
		payload := bytes.Repeat([]byte{0xAB}, n)
		for _, masked := range []bool{true, false} {
			frame, err := encodeFrame(opBinary, payload, masked)
			if err != nil {
				t.Fatal(err)
			}
			fin, op, got, err := readFrame(bytes.NewReader(frame))
			if err != nil {
				t.Fatalf("n=%d masked=%v: %v", n, masked, err)
			}
			if !fin || op != opBinary || !bytes.Equal(got, payload) {
				t.Fatalf("n=%d masked=%v: fin=%v op=%d len=%d", n, masked, fin, op, len(got))
			}
		}
	}
}

func TestReadFrameRefusesReservedBitsAndHugeFrames(t *testing.T) {
	if _, _, _, err := readFrame(bytes.NewReader([]byte{0xC2, 0x00})); err == nil {
		t.Fatal("reserved bit accepted")
	}
	huge := []byte{0x82, 127, 0, 0, 0, 0, 0x10, 0, 0, 0}
	if _, _, _, err := readFrame(bytes.NewReader(huge)); err == nil {
		t.Fatal("frame over the cap accepted")
	}
}

func TestParseExitStatus(t *testing.T) {
	code, err := parseExitStatus([]byte(`{"metadata":{},"status":"Success"}`))
	if code != 0 || err != nil {
		t.Fatalf("success: %d %v", code, err)
	}
	code, err = parseExitStatus([]byte(`{"status":"Failure","message":"command terminated with non-zero exit code: error executing command [sh -c exit 3], exit code 3","reason":"NonZeroExitCode","details":{"causes":[{"reason":"ExitCode","message":"3"}]}}`))
	var ee *ExitError
	if code != 3 || err == nil || !asExit(err, &ee) || ee.Code != 3 {
		t.Fatalf("non-zero: %d %v", code, err)
	}
	if _, err := parseExitStatus([]byte(`{"status":"Failure","message":"container not found (\"agent\")","reason":"InternalError"}`)); err == nil || asExit(err, &ee) {
		t.Fatalf("a failure to start must be an error, not an exit code: %v", err)
	}
	if _, err := parseExitStatus([]byte(`not json`)); err == nil {
		t.Fatal("garbage status accepted")
	}
}

func asExit(err error, ee **ExitError) bool {
	e, ok := err.(*ExitError)
	if ok {
		*ee = e
	}
	return ok
}

// fakeStream is a server-side WebSocket session of the fake API server.
type fakeStream struct {
	rw    *bufio.ReadWriter
	conn  io.Closer
	proto string
	mu    sync.Mutex
}

// accept completes the server side of the handshake.
func acceptWS(w http.ResponseWriter, r *http.Request, protos []string) (*fakeStream, bool) {
	offered := strings.Split(strings.Join(r.Header.Values("Sec-WebSocket-Protocol"), ","), ",")
	pick := ""
	for _, want := range protos {
		for _, o := range offered {
			if strings.TrimSpace(o) == want && pick == "" {
				pick = want
			}
		}
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || pick == "" {
		http.Error(w, `{"kind":"Status","reason":"BadRequest","message":"no upgrade"}`, http.StatusBadRequest)
		return nil, false
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", 500)
		return nil, false
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, false
	}
	accept := wsAccept(r.Header.Get("Sec-WebSocket-Key"))
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\nSec-WebSocket-Protocol: " + pick + "\r\n\r\n")
	rw.Flush()
	return &fakeStream{rw: rw, conn: conn, proto: pick}, true
}

func (s *fakeStream) send(stream byte, p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, _ := encodeFrame(opBinary, append([]byte{stream}, p...), false)
	s.rw.Write(f)
	s.rw.Flush()
}

// readStdin reads client frames until stdin is closed (v5) and returns it.
func (s *fakeStream) readStdin(t *testing.T) []byte {
	var in []byte
	for {
		fin, op, p, err := readFrame(s.rw)
		if err != nil {
			return in
		}
		if op == opClose {
			return in
		}
		if !fin || op != opBinary || len(p) == 0 {
			continue
		}
		switch p[0] {
		case streamStdin:
			in = append(in, p[1:]...)
		case streamClose:
			return in
		}
	}
}

func (s *fakeStream) finish(status string) {
	s.send(streamError, []byte(status))
	s.mu.Lock()
	f, _ := encodeFrame(opClose, []byte{0x03, 0xE8}, false)
	s.rw.Write(f)
	s.rw.Flush()
	s.mu.Unlock()
	s.conn.Close()
}

// testClient returns a client for srv, trusting its certificate, with a
// token, through a kubeconfig written to a temp folder.
func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	dir := t.TempDir()
	kc := `apiVersion: v1
kind: Config
current-context: test
clusters:
- name: c
  cluster:
    server: ` + srv.URL + `
    certificate-authority: ca.pem
contexts:
- name: test
  context: {cluster: c, user: u, namespace: work}
users:
- name: u
  user: {token: secret-token}
`
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p, []byte(kc), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := FromKubeconfig([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestExecOverWebSocket(t *testing.T) {
	var gotAuth, gotCmd string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCmd = strings.Join(r.URL.Query()["command"], " ")
		s, ok := acceptWS(w, r, []string{protoV5})
		if !ok {
			return
		}
		in := s.readStdin(t)
		s.send(streamStdout, bytes.ToUpper(in))
		s.send(streamStderr, []byte("warn\n"))
		if bytes.Contains(in, []byte("fail")) {
			s.finish(`{"status":"Failure","reason":"NonZeroExitCode","message":"exit 7","details":{"causes":[{"reason":"ExitCode","message":"7"}]}}`)
			return
		}
		s.finish(`{"status":"Success"}`)
	}))
	defer srv.Close()
	c := testClient(t, srv)
	var out, errb bytes.Buffer
	code, err := c.Exec(t.Context(), "ns", "pod", "ctr", []string{"cat", "-"}, StreamOptions{Stdin: strings.NewReader("hello stdin"), Stdout: &out, Stderr: &errb})
	if err != nil || code != 0 {
		t.Fatalf("exec: %d %v", code, err)
	}
	if out.String() != "HELLO STDIN" || errb.String() != "warn\n" {
		t.Fatalf("streams: %q %q", out.String(), errb.String())
	}
	if gotAuth != "Bearer secret-token" || gotCmd != "cat -" {
		t.Fatalf("request: auth %q cmd %q", gotAuth, gotCmd)
	}
	code, err = c.Exec(t.Context(), "ns", "pod", "ctr", []string{"x"}, StreamOptions{Stdin: strings.NewReader("fail")})
	if err != nil || code != 7 {
		t.Fatalf("non-zero exit: %d %v", code, err)
	}
	if _, err := c.ExecOutput(t.Context(), "ns", "pod", "ctr", []string{"x"}, strings.NewReader("fail"), 1024); err == nil || !strings.Contains(err.Error(), "warn") {
		t.Fatalf("ExecOutput must carry stderr of a failed command: %v", err)
	}
}

func TestExecWithStdinNeedsV5(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := acceptWS(w, r, []string{protoV4})
		if !ok {
			return
		}
		s.finish(`{"status":"Success"}`)
	}))
	defer srv.Close()
	c := testClient(t, srv)
	if _, err := c.Exec(t.Context(), "ns", "pod", "ctr", []string{"x"}, StreamOptions{Stdin: strings.NewReader("x")}); err == nil || !strings.Contains(err.Error(), "1.30") {
		t.Fatalf("v4 with stdin: %v", err)
	}
	if code, err := c.Exec(t.Context(), "ns", "pod", "ctr", []string{"x"}, StreamOptions{}); err != nil || code != 0 {
		t.Fatalf("v4 without stdin: %d %v", code, err)
	}
}

func TestExecStreamWithoutStatusIsAnError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := acceptWS(w, r, []string{protoV5})
		if !ok {
			return
		}
		s.send(streamStdout, []byte("partial"))
		s.conn.Close()
	}))
	defer srv.Close()
	c := testClient(t, srv)
	if _, err := c.Exec(t.Context(), "ns", "pod", "ctr", []string{"x"}, StreamOptions{}); err == nil {
		t.Fatal("a stream cut before its status must not read as success")
	}
}

func TestDoDecodesStatusErrors(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "reason": "AlreadyExists", "message": `namespaces "x" already exists`})
	}))
	defer srv.Close()
	c := testClient(t, srv)
	err := c.Create(t.Context(), "/api/v1/namespaces", map[string]any{}, nil)
	if !IsConflict(err) || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("conflict: %v", err)
	}
}

func TestKubeconfigRefusals(t *testing.T) {
	dir := t.TempDir()
	write := func(user string) string {
		p := filepath.Join(dir, "cfg")
		os.WriteFile(p, []byte(`current-context: x
clusters: [{name: c, cluster: {server: "https://127.0.0.1:1"}}]
contexts: [{name: x, context: {cluster: c, user: u}}]
users: [{name: u, user: `+user+`}]
`), 0o600)
		return p
	}
	for _, tc := range []struct{ user, want string }{
		{`{exec: {command: aws}}`, "exec plugin"},
		{`{auth-provider: {name: gcp}}`, "auth-provider"},
		{`{username: a, password: b}`, "basic auth"},
	} {
		if _, err := FromKubeconfig([]string{write(tc.user)}, ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.user, err)
		}
	}
	p := filepath.Join(dir, "insecure")
	os.WriteFile(p, []byte(`current-context: x
clusters: [{name: c, cluster: {server: "https://127.0.0.1:1", insecure-skip-tls-verify: true}}]
contexts: [{name: x, context: {cluster: c, user: u}}]
users: [{name: u, user: {token: t}}]
`), 0o600)
	if _, err := FromKubeconfig([]string{p}, ""); err == nil || !strings.Contains(err.Error(), "insecure") {
		t.Errorf("insecure: %v", err)
	}
	p = filepath.Join(dir, "plain")
	os.WriteFile(p, []byte(`current-context: x
clusters: [{name: c, cluster: {server: "http://127.0.0.1:1"}}]
contexts: [{name: x, context: {cluster: c, user: u}}]
users: [{name: u, user: {token: t}}]
`), 0o600)
	if _, err := FromKubeconfig([]string{p}, ""); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("http server: %v", err)
	}
	if _, err := FromKubeconfig([]string{p}, "missing"); err == nil || !strings.Contains(err.Error(), "no context") {
		t.Errorf("missing context: %v", err)
	}
}

func TestKubeconfigMergeAndTokenFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	os.WriteFile(a, []byte(`current-context: first
contexts: [{name: first, context: {cluster: c, user: u, namespace: ns1}}]
`), 0o600)
	os.WriteFile(filepath.Join(dir, "tok"), []byte("file-token\n"), 0o600)
	os.WriteFile(b, []byte(`current-context: second
clusters: [{name: c, cluster: {server: "https://127.0.0.1:6443"}}]
contexts: [{name: first, context: {cluster: other, user: other}}]
users: [{name: u, user: {tokenFile: tok}}]
`), 0o600)
	c, err := FromKubeconfig([]string{a, b}, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Context != "first" || c.Namespace != "ns1" || c.Server != "https://127.0.0.1:6443" {
		t.Fatalf("merge: %+v", c)
	}
	if c.bearer() != "file-token" {
		t.Fatalf("token file relative to its kubeconfig: %q", c.bearer())
	}
}

func TestKubeconfigClientCertificate(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"major":"1","minor":"35","gitVersion":"v1.35.0"}`))
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	srv.StartTLS()
	defer srv.Close()
	cert := srv.TLS.Certificates[0]
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	enc := func(b []byte) string { return b64(b) }
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg")
	os.WriteFile(p, []byte(`current-context: x
clusters: [{name: c, cluster: {server: "`+srv.URL+`", certificate-authority-data: "`+enc(certPEM)+`"}}]
contexts: [{name: x, context: {cluster: c, user: u}}]
users: [{name: u, user: {client-certificate-data: "`+enc(certPEM)+`", client-key-data: "`+enc(keyPEM)+`"}}]
`), 0o600)
	c, err := FromKubeconfig([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.ServerVersion(t.Context())
	if err != nil || v.GitVersion != "v1.35.0" {
		t.Fatalf("version with a client certificate: %v %v", v, err)
	}
	if ok, _ := versionOK(v); !ok {
		t.Fatal("1.35 must pass the version check")
	}
	if ok, why := versionOK(&Version{Major: "1", Minor: "28+", GitVersion: "v1.28.3"}); ok || !strings.Contains(why, "1.30") {
		t.Fatalf("1.28: %v %q", ok, why)
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
