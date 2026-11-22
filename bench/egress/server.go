package egress

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Outcomes in the request log.
const (
	OutcomeHit      = "hit"      // replayed from the cache
	OutcomeMiss     = "miss"     // replay had no recording: 404, counted as egress_unmatched
	OutcomeRefused  = "refused"  // offline, or a host off the allowlist: 403
	OutcomeLive     = "live"     // live-allowlisted: forwarded, not recorded
	OutcomeRecorded = "recorded" // record: forwarded and written
	OutcomeError    = "error"    // the upstream could not be reached
)

// LogRow is one line of the request log.
type LogRow struct {
	Time    string `json:"time"`
	Mode    string `json:"mode"`
	Method  string `json:"method"`
	URL     string `json:"url"`
	Outcome string `json:"outcome"`
	Status  int    `json:"status"`
	Bytes   int64  `json:"bytes"`
	Detail  string `json:"detail,omitempty"`
}

// Server is the proxy. Set the fields, then serve it with net/http.
type Server struct {
	Mode      string
	CA        *CA
	Cache     *Cache    // replay (and the default allowlist of live)
	Recorder  *Recorder // record
	Allow     []string  // live-allowlisted and record; empty in record means every host
	Log       io.Writer // request log, one JSON row per request; nil for none
	Transport http.RoundTripper
	// MaxRequestBody caps a request body the proxy reads to hash and
	// forward; default 64 MiB.
	MaxRequestBody int64
	// Rewrite maps a URL prefix to another for the upstream fetch only
	// (record and live): the recording keeps the URL the client asked for.
	// Used to fetch a dated archive, for example deb.debian.org from
	// snapshot.debian.org, when a task pins versions the live mirror no
	// longer serves.
	Rewrite []Rewrite

	logMu  sync.Mutex
	counts [6]atomic.Int64
	now    func() time.Time
}

// Counts returns how many requests ended in each outcome.
func (s *Server) Counts() map[string]int64 {
	out := map[string]int64{}
	for i, o := range outcomes {
		out[o] = s.counts[i].Load()
	}
	return out
}

var outcomes = []string{OutcomeHit, OutcomeMiss, OutcomeRefused, OutcomeLive, OutcomeRecorded, OutcomeError}

// NewTransport is the upstream transport: no proxy of its own and no
// transparent decompression, so bodies are stored exactly as sent.
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DisableCompression = true
	t.ResponseHeaderTimeout = 120 * time.Second
	t.MaxIdleConnsPerHost = 8
	return t
}

// ServeHTTP handles CONNECT tunnels, absolute-URL proxy requests and the
// service's own paths (/healthz, /ca.crt, /stats).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodConnect:
		s.connect(w, r)
	case r.URL.IsAbs():
		s.handle(w, r, r.URL.String())
	case r.URL.Path == "/healthz":
		fmt.Fprintln(w, "ok", s.Mode)
	case r.URL.Path == "/ca.crt" && s.CA != nil:
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(s.CA.CertPEM())
	case r.URL.Path == "/stats":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"mode": s.Mode, "counts": s.Counts()})
	default:
		http.Error(w, "heimdall egress: this is a forward proxy; set HTTP_PROXY and HTTPS_PROXY to it", http.StatusBadRequest)
	}
}

// connect answers a CONNECT, then serves HTTP over TLS on the tunnel with a
// certificate for the requested host.
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if s.CA == nil {
		s.logRow(r.Method, "https://"+host, OutcomeRefused, http.StatusForbidden, 0, "no CA loaded")
		http.Error(w, "heimdall egress: HTTPS needs the task's CA, which this service has not loaded", http.StatusForbidden)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		conn.Close()
		return
	}
	hostOnly, port, err := net.SplitHostPort(host)
	if err != nil {
		hostOnly, port = host, "443"
	}
	var inner net.Conn = conn
	if buf != nil && buf.Reader.Buffered() > 0 {
		inner = &prefixConn{Conn: conn, r: io.MultiReader(bytes.NewReader(peek(buf.Reader)), conn)}
	}
	tconn := tls.Server(inner, &tls.Config{
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := h.ServerName
			if name == "" {
				name = hostOnly
			}
			return s.CA.Leaf(name)
		},
	})
	base := "https://" + hostOnly
	if port != "443" {
		base += ":" + port
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, ir *http.Request) {
			s.handle(w, ir, base+ir.URL.RequestURI())
		}),
		ReadHeaderTimeout: 60 * time.Second,
		ErrorLog:          nil,
	}
	l := &oneConnListener{c: tconn, done: make(chan struct{})}
	go func() {
		_ = srv.Serve(l)
	}()
}

func peek(r *bufio.Reader) []byte {
	b, _ := r.Peek(r.Buffered())
	return append([]byte(nil), b...)
}

type prefixConn struct {
	net.Conn
	r io.Reader
}

func (p *prefixConn) Read(b []byte) (int, error) { return p.r.Read(b) }

// oneConnListener hands one connection to http.Server, then blocks until
// that connection closes, so Serve keeps it.
type oneConnListener struct {
	c    net.Conn
	once sync.Once
	done chan struct{}
	used atomic.Bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.used.CompareAndSwap(false, true) {
		return &closeNotify{Conn: l.c, l: l}, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *oneConnListener) Addr() net.Addr { return l.c.LocalAddr() }

type closeNotify struct {
	net.Conn
	l *oneConnListener
}

func (c *closeNotify) Close() error {
	err := c.Conn.Close()
	c.l.Close()
	return err
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func stripHop(h http.Header) {
	for _, f := range h.Values("Connection") {
		for _, k := range strings.Split(f, ",") {
			h.Del(strings.TrimSpace(k))
		}
	}
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

// handle serves one request for target (an absolute URL) in the server's
// mode.
func (s *Server) handle(w http.ResponseWriter, r *http.Request, target string) {
	target = NormalizeURL(target)
	limit := s.MaxRequestBody
	if limit <= 0 {
		limit = 64 << 20
	}
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil || int64(len(b)) > limit {
			s.logRow(r.Method, target, OutcomeRefused, http.StatusRequestEntityTooLarge, 0, "request body over the cap or unreadable")
			http.Error(w, "heimdall egress: request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		body = b
	}
	bodySHA := ""
	if len(body) > 0 {
		sum := sha256.Sum256(body)
		bodySHA = hex.EncodeToString(sum[:])
	}
	host := r.URL.Hostname()
	if u := hostOf(target); u != "" {
		host = u
	}
	switch s.Mode {
	case ModeReplay:
		s.replay(w, r, target, bodySHA)
	case ModeLive:
		allow := s.Allow
		if len(allow) == 0 && s.Cache != nil {
			allow = Hosts(s.Cache.Entries())
		}
		if !Allowed(allow, host) {
			s.refuse(w, r.Method, target, "host "+host+" is not on the allowlist")
			return
		}
		s.forward(w, r, target, body, bodySHA, false)
	case ModeRecord:
		if len(s.Allow) > 0 && !Allowed(s.Allow, host) {
			s.refuse(w, r.Method, target, "host "+host+" is not on the allowlist")
			return
		}
		s.forward(w, r, target, body, bodySHA, true)
	default:
		s.refuse(w, r.Method, target, "offline: this run has no internet")
	}
}

func hostOf(target string) string {
	i := strings.Index(target, "://")
	if i < 0 {
		return ""
	}
	rest := target[i+3:]
	if j := strings.IndexAny(rest, "/?"); j >= 0 {
		rest = rest[:j]
	}
	if h, _, err := net.SplitHostPort(rest); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(rest, "[]")
}

func (s *Server) refuse(w http.ResponseWriter, method, target, why string) {
	s.logRow(method, target, OutcomeRefused, http.StatusForbidden, 0, why)
	w.Header().Set("X-Heimdall-Egress", OutcomeRefused)
	http.Error(w, "heimdall egress ("+s.Mode+"): "+why, http.StatusForbidden)
}

func (s *Server) replay(w http.ResponseWriter, r *http.Request, target, bodySHA string) {
	if s.Cache == nil {
		s.logRow(r.Method, target, OutcomeMiss, http.StatusNotFound, 0, "no cache loaded")
		w.Header().Set("X-Heimdall-Egress", OutcomeMiss)
		http.Error(w, "heimdall egress (replay): no recorded response for "+r.Method+" "+target, http.StatusNotFound)
		return
	}
	e, sec, ok := s.Cache.Lookup(r.Method, target, bodySHA)
	if !ok {
		s.logRow(r.Method, target, OutcomeMiss, http.StatusNotFound, 0, "")
		w.Header().Set("X-Heimdall-Egress", OutcomeMiss)
		http.Error(w, "heimdall egress (replay): no recorded response for "+r.Method+" "+target, http.StatusNotFound)
		return
	}
	h := w.Header()
	for k, vs := range e.Header {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	stripHop(h)
	if r.Method != http.MethodHead {
		h.Set("Content-Length", strconv.FormatInt(e.Size, 10))
	}
	w.WriteHeader(e.Status)
	var n int64
	if r.Method != http.MethodHead && bodyAllowed(e.Status) {
		n, _ = io.Copy(w, sec)
	}
	s.logRow(r.Method, target, OutcomeHit, e.Status, n, "")
}

func bodyAllowed(status int) bool {
	return status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified
}

func isRedirect(code int) bool {
	return code == http.StatusMovedPermanently || code == http.StatusFound || code == http.StatusSeeOther ||
		code == http.StatusTemporaryRedirect || code == http.StatusPermanentRedirect
}

// Rewrite is one upstream URL prefix rewrite.
type Rewrite struct{ From, To string }

// ParseRewrites reads "from=to" pairs.
func ParseRewrites(list []string) ([]Rewrite, error) {
	var out []Rewrite
	for _, p := range list {
		from, to, ok := strings.Cut(p, "=")
		if !ok || !strings.Contains(from, "://") || !strings.Contains(to, "://") {
			return nil, fmt.Errorf("rewrite %q: want from-url-prefix=to-url-prefix", p)
		}
		out = append(out, Rewrite{From: NormalizeURL(from), To: to})
	}
	return out, nil
}

func (s *Server) upstreamURL(target string) string {
	for _, rw := range s.Rewrite {
		if strings.HasPrefix(target, rw.From) {
			return rw.To + strings.TrimPrefix(target, rw.From)
		}
	}
	return target
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request, target string, body []byte, bodySHA string, record bool) {
	out, err := http.NewRequestWithContext(r.Context(), r.Method, s.upstreamURL(target), bytes.NewReader(body))
	if err != nil {
		s.logRow(r.Method, target, OutcomeError, http.StatusBadGateway, 0, err.Error())
		http.Error(w, "heimdall egress: "+err.Error(), http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	stripHop(out.Header)
	// Conditional requests would record a 304 that only makes sense against
	// the client's local copy at record time; always fetch the full body.
	for _, k := range []string{"If-Modified-Since", "If-None-Match", "If-Range", "If-Unmodified-Since", "If-Match"} {
		out.Header.Del(k)
	}
	out.ContentLength = int64(len(body))
	if len(body) == 0 {
		out.Body = nil
	}
	tr := s.Transport
	if tr == nil {
		tr = NewTransport()
		s.Transport = tr
	}
	resp, err := tr.RoundTrip(out)
	// A rewritten fetch follows the other archive's redirects itself: the
	// client asked for the original URL and must get the body there.
	for hops := 0; err == nil && out.URL.String() != target && hops < 10 && isRedirect(resp.StatusCode) && (r.Method == http.MethodGet || r.Method == http.MethodHead); hops++ {
		loc, lerr := resp.Location()
		if lerr != nil {
			break
		}
		resp.Body.Close()
		next := out.Clone(r.Context())
		next.URL, next.Host, next.Body, next.ContentLength = loc, "", nil, 0
		out = next
		resp, err = tr.RoundTrip(out)
	}
	if err != nil {
		s.logRow(r.Method, target, OutcomeError, http.StatusBadGateway, 0, err.Error())
		http.Error(w, "heimdall egress: upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	hdr := resp.Header.Clone()
	stripHop(hdr)
	for k, vs := range hdr {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	outcome := OutcomeLive
	var blob *Blob
	if record && s.Recorder != nil && !s.Recorder.Has(r.Method, target, bodySHA) {
		if blob, err = s.Recorder.NewBlob(); err != nil {
			blob = nil
		}
	}
	var dst io.Writer = w
	if blob != nil {
		dst = io.MultiWriter(w, blob)
	}
	n, copyErr := io.Copy(dst, resp.Body)
	detail := ""
	if copyErr != nil {
		detail = copyErr.Error()
	}
	if record {
		outcome = OutcomeRecorded
	}
	if blob != nil {
		if copyErr != nil || (resp.ContentLength >= 0 && n != resp.ContentLength) {
			blob.Discard()
			outcome, detail = OutcomeError, "body cut short; not recorded: "+detail
		} else {
			if r.Method != http.MethodHead {
				hdr.Del("Content-Length")
			}
			e := Entry{Method: r.Method, URL: target, BodySHA256: bodySHA, Status: resp.StatusCode, Header: hdr}
			if _, err := blob.Commit(e); err != nil {
				outcome, detail = OutcomeError, "not recorded: "+err.Error()
			}
		}
	}
	s.logRow(r.Method, target, outcome, resp.StatusCode, n, detail)
}

func (s *Server) logRow(method, target, outcome string, status int, n int64, detail string) {
	for i, o := range outcomes {
		if o == outcome {
			s.counts[i].Add(1)
		}
	}
	if s.Log == nil {
		return
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	row, err := json.Marshal(LogRow{Time: now().UTC().Format(time.RFC3339Nano), Mode: s.Mode, Method: method, URL: target, Outcome: outcome, Status: status, Bytes: n, Detail: detail})
	if err != nil {
		return
	}
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.Log.Write(append(row, '\n'))
}
