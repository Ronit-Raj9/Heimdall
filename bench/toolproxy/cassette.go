package toolproxy

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"
)

// A cassette is the recorded traffic of one external API: a JSONL file, one
// CassetteEntry per call, written by a record-mode run and read by a replay
// run. A replay answers a call from the entries with the same method, path,
// normalized query and request body hash, without any network.

// cassetteHeaders are the response headers a cassette keeps. Everything else
// (cookies, dates, server names, rate-limit counters) is dropped, so a
// cassette is small and holds nothing session-specific.
var cassetteHeaders = []string{"Content-Type", "Content-Language", "Location", "Retry-After", "Etag", "Last-Modified"}

// maxCassetteLine bounds one cassette line when it is read.
const maxCassetteLine = 64 << 20

// CassetteEntry is one recorded call.
type CassetteEntry struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`        // after the base URL, e.g. /rates
	Query      string            `json:"query"`       // NormalizeQuery of the raw query
	BodySHA256 string            `json:"body_sha256"` // of the raw request body
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers,omitempty"` // the cassetteHeaders the answer had
	Body       string            `json:"body"`
	Base64     bool              `json:"base64,omitempty"` // Body is base64 because the answer was not UTF-8 text
}

// NewCassetteEntry builds the entry of one call. reqBody is hashed; body is
// the (already redacted) answer, kept as text when it is UTF-8 and as base64
// otherwise.
func NewCassetteEntry(method, path, rawQuery string, reqBody []byte, status int, h http.Header, body []byte) CassetteEntry {
	e := CassetteEntry{Method: method, Path: path, Query: NormalizeQuery(rawQuery), BodySHA256: sha256Hex(reqBody), Status: status}
	for _, k := range cassetteHeaders {
		if v := h.Get(k); v != "" {
			if e.Headers == nil {
				e.Headers = map[string]string{}
			}
			e.Headers[k] = v
		}
	}
	if utf8.Valid(body) {
		e.Body = string(body)
	} else {
		e.Body, e.Base64 = base64.StdEncoding.EncodeToString(body), true
	}
	return e
}

// BodyBytes returns the recorded answer body.
func (e CassetteEntry) BodyBytes() ([]byte, error) {
	if !e.Base64 {
		return []byte(e.Body), nil
	}
	b, err := base64.StdEncoding.DecodeString(e.Body)
	if err != nil {
		return nil, fmt.Errorf("decode cassette body of %s %s: %w", e.Method, e.Path, err)
	}
	return b, nil
}

// NormalizeQuery puts a raw query in a canonical form (keys sorted, values
// in their order, standard escaping) so the same call matches however the
// agent ordered its parameters. A query that does not parse is kept as it
// is. It is pure.
func NormalizeQuery(raw string) string {
	if raw == "" {
		return ""
	}
	v, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	return v.Encode()
}

// ReadCassette parses a cassette. Unlike the ledger it is strict: a line
// that does not parse, or an entry without a method, a path or a status, is
// an error naming the line, because a replay must not run on half a cassette.
func ReadCassette(r io.Reader) ([]CassetteEntry, error) {
	var out []CassetteEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxCassetteLine)
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e CassetteEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("cassette line %d: %w", n, err)
		}
		if e.Method == "" || e.Path == "" || e.Status < 100 || e.Status > 999 {
			return nil, fmt.Errorf("cassette line %d: needs a method, a path and a status", n)
		}
		if _, err := e.BodyBytes(); err != nil {
			return nil, fmt.Errorf("cassette line %d: %w", n, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read cassette: %w", err)
	}
	return out, nil
}

// cassette is a loaded cassette, ready to answer replayed calls. Entries
// that share a key are served in recorded order and the last one repeats,
// so a call made more often than it was recorded still gets an answer.
type cassette struct {
	mu    sync.Mutex
	byKey map[string][]CassetteEntry
	next  map[string]int
}

// loadCassette reads the cassette at path.
func loadCassette(path string) (*cassette, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open cassette %s: %w", path, err)
	}
	defer f.Close()
	entries, err := ReadCassette(f)
	if err != nil {
		return nil, fmt.Errorf("cassette %s: %w", path, err)
	}
	return newCassette(entries), nil
}

func newCassette(entries []CassetteEntry) *cassette {
	c := &cassette{byKey: map[string][]CassetteEntry{}, next: map[string]int{}}
	for _, e := range entries {
		k := cassetteKey(e.Method, e.Path, e.Query, e.BodySHA256)
		c.byKey[k] = append(c.byKey[k], e)
	}
	return c
}

// match returns the entry that answers a call, if any.
func (c *cassette) match(method, path, rawQuery string, body []byte) (CassetteEntry, bool) {
	k := cassetteKey(method, path, NormalizeQuery(rawQuery), sha256Hex(body))
	c.mu.Lock()
	defer c.mu.Unlock()
	es := c.byKey[k]
	if len(es) == 0 {
		return CassetteEntry{}, false
	}
	i := min(c.next[k], len(es)-1)
	c.next[k] = i + 1
	return es[i], true
}

func cassetteKey(method, path, query, bodySHA string) string {
	return method + " " + path + "?" + query + "#" + bodySHA
}

// cassetteWriter appends entries to a record-mode cassette, one line each,
// fsynced, so a run that dies keeps what it recorded.
type cassetteWriter struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// openCassetteWriter creates the cassette file (empty) and its folder, or
// opens an existing one for append, so the environment kernel always finds
// a file to export.
func openCassetteWriter(path string) (*cassetteWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create cassette dir for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open cassette %s for record: %w", path, err)
	}
	return &cassetteWriter{path: path, f: f}, nil
}

// Append writes one entry.
func (w *cassetteWriter) Append(e CassetteEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode cassette entry: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write cassette %s: %w", w.path, err)
	}
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("sync cassette %s: %w", w.path, err)
	}
	return nil
}

// Close closes the file.
func (w *cassetteWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("close cassette %s: %w", w.path, err)
	}
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
