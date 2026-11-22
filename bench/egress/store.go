package egress

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one recorded response.
type Entry struct {
	Method     string              `json:"method"`
	URL        string              `json:"url"`
	BodySHA256 string              `json:"body_sha256,omitempty"` // of the request body; empty when it had none
	Status     int                 `json:"status"`
	Header     map[string][]string `json:"header,omitempty"`
	Blob       string              `json:"blob"` // sha256 of the response body, its name under blobs/
	Size       int64               `json:"size"`
}

// Key is how a request is looked up: method, normalised URL and the
// request body's hash. Headers are not part of it.
func (e Entry) Key() string { return requestKey(e.Method, e.URL, e.BodySHA256) }

func requestKey(method, u, body string) string { return method + " " + u + " " + body }

func looseKey(method, u string) string { return method + " " + u }

// NormalizeURL lowercases scheme and host and drops default ports, so the
// same request reached through CONNECT or as an absolute URL has one key.
func NormalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		host += ":" + port
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	u.Fragment = ""
	u.User = nil
	return u.String()
}

// Hosts lists the distinct host names of entries.
func Hosts(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		if u, err := url.Parse(e.URL); err == nil && u.Hostname() != "" {
			out = append(out, strings.ToLower(u.Hostname()))
		}
	}
	sort.Strings(out)
	return dedupe(out)
}

// Recorder writes responses into a folder: blobs/<sha256> and one
// manifest.jsonl row per new request, appended and synced as it goes so a
// stopped container loses nothing finished.
type Recorder struct {
	dir      string
	maxBytes int64

	mu    sync.Mutex
	seen  map[string]bool
	total int64
	mf    *os.File
}

// NewRecorder opens (or continues) a record folder. maxBytes caps the sum
// of new blob sizes; 0 means no cap.
func NewRecorder(dir string, maxBytes int64) (*Recorder, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return nil, err
	}
	r := &Recorder{dir: dir, maxBytes: maxBytes, seen: map[string]bool{}}
	old, _ := ReadManifest(filepath.Join(dir, "manifest.jsonl"))
	for _, e := range old {
		r.seen[e.Key()] = true
	}
	f, err := os.OpenFile(filepath.Join(dir, "manifest.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	r.mf = f
	return r, nil
}

// Close closes the manifest.
func (r *Recorder) Close() error { return r.mf.Close() }

// Has reports whether a request is already recorded.
func (r *Recorder) Has(method, u, body string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[requestKey(method, u, body)]
}

// Blob is one response body being written.
type Blob struct {
	r    *Recorder
	f    *os.File
	h    io.Writer
	sum  interface{ Sum([]byte) []byte }
	n    int64
	fail bool
}

// NewBlob starts a response body.
func (r *Recorder) NewBlob() (*Blob, error) {
	f, err := os.CreateTemp(filepath.Join(r.dir, "blobs"), ".part-*")
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	return &Blob{r: r, f: f, h: h, sum: h}, nil
}

// Write implements io.Writer; it never fails the caller's copy, it only
// marks the blob as not recordable.
func (b *Blob) Write(p []byte) (int, error) {
	if b.fail {
		return len(p), nil
	}
	if _, err := b.f.Write(p); err != nil {
		b.fail = true
		return len(p), nil
	}
	b.h.Write(p)
	b.n += int64(len(p))
	return len(p), nil
}

// Discard throws the blob away.
func (b *Blob) Discard() {
	name := b.f.Name()
	b.f.Close()
	os.Remove(name)
}

// Commit stores the blob and appends e (Blob and Size filled in). It
// returns false when the request was recorded already or the cap is hit.
func (b *Blob) Commit(e Entry) (bool, error) {
	name := b.f.Name()
	if b.fail {
		b.Discard()
		return false, errors.New("write blob failed")
	}
	if err := b.f.Close(); err != nil {
		os.Remove(name)
		return false, err
	}
	e.Blob, e.Size = hex.EncodeToString(b.sum.Sum(nil)), b.n
	r := b.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[e.Key()] {
		os.Remove(name)
		return false, nil
	}
	final := filepath.Join(r.dir, "blobs", e.Blob)
	if _, err := os.Stat(final); err == nil {
		os.Remove(name)
	} else {
		if r.maxBytes > 0 && r.total+e.Size > r.maxBytes {
			os.Remove(name)
			return false, fmt.Errorf("record cap of %d bytes reached", r.maxBytes)
		}
		if err := os.Rename(name, final); err != nil {
			os.Remove(name)
			return false, err
		}
		r.total += e.Size
	}
	line, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	if _, err := r.mf.Write(append(line, '\n')); err != nil {
		return false, err
	}
	if err := r.mf.Sync(); err != nil {
		return false, err
	}
	r.seen[e.Key()] = true
	return true, nil
}

// ReadManifest reads manifest.jsonl rows.
func ReadManifest(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseManifest(f)
}

func parseManifest(r io.Reader) ([]Entry, error) {
	var out []Entry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("manifest line %d: %w", n, err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Pack writes a record folder as a cache tar: manifest.jsonl first (rows
// sorted by key, one per key), then blobs/<sha256> sorted, with fixed
// owners and times so the same recording packs to the same bytes. Blobs no
// row names are left out. It returns the rows written.
func Pack(dir string, w io.Writer) ([]Entry, error) {
	rows, err := ReadManifest(filepath.Join(dir, "manifest.jsonl"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	byKey := map[string]Entry{}
	for _, e := range rows {
		if _, ok := byKey[e.Key()]; !ok {
			byKey[e.Key()] = e
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var mf bytes.Buffer
	blobs := map[string]bool{}
	out := make([]Entry, 0, len(keys))
	for _, k := range keys {
		e := byKey[k]
		line, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		mf.Write(append(line, '\n'))
		blobs[e.Blob] = true
		out = append(out, e)
	}
	tw := tar.NewWriter(w)
	epoch := time.Unix(0, 0).UTC()
	hdr := func(name string, size int64) *tar.Header {
		return &tar.Header{Name: name, Mode: 0o644, Size: size, ModTime: epoch, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	}
	if err := tw.WriteHeader(hdr("manifest.jsonl", int64(mf.Len()))); err != nil {
		return nil, err
	}
	if _, err := tw.Write(mf.Bytes()); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(blobs))
	for b := range blobs {
		names = append(names, b)
	}
	sort.Strings(names)
	for _, b := range names {
		f, err := os.Open(filepath.Join(dir, "blobs", b))
		if err != nil {
			return nil, fmt.Errorf("blob %s: %w", b, err)
		}
		fi, err := f.Stat()
		if err == nil {
			err = tw.WriteHeader(hdr("blobs/"+b, fi.Size()))
		}
		if err == nil {
			_, err = io.Copy(tw, f)
		}
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("blob %s: %w", b, err)
		}
	}
	return out, tw.Close()
}

// EmptyCache is the cache tar of a recording with no rows.
func EmptyCache() []byte {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	_ = tw.WriteHeader(&tar.Header{Name: "manifest.jsonl", Mode: 0o644, Size: 0, ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg, Format: tar.FormatPAX})
	_ = tw.Close()
	return b.Bytes()
}

// Cache is a cache tar opened for replay. Bodies are read in place.
type Cache struct {
	f       *os.File
	entries []Entry
	byKey   map[string]int
	byURL   map[string]int
	blobs   map[string]blobRef
}

type blobRef struct{ off, size int64 }

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// OpenCache indexes a cache tar.
func OpenCache(path string) (*Cache, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	c := &Cache{f: f, byKey: map[string]int{}, byURL: map[string]int{}, blobs: map[string]blobRef{}}
	cr := &countingReader{r: f}
	tr := tar.NewReader(cr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("cache %s: %w", path, err)
		}
		switch {
		case h.Name == "manifest.jsonl":
			rows, err := parseManifest(tr)
			if err != nil {
				f.Close()
				return nil, err
			}
			c.entries = rows
		case strings.HasPrefix(h.Name, "blobs/") && h.Typeflag == tar.TypeReg:
			// archive/tar reads exactly the header blocks before Next
			// returns, so the data starts where the reader stands.
			c.blobs[strings.TrimPrefix(h.Name, "blobs/")] = blobRef{off: cr.n, size: h.Size}
		}
	}
	for i, e := range c.entries {
		if _, ok := c.blobs[e.Blob]; !ok {
			f.Close()
			return nil, fmt.Errorf("cache %s: row %s %s names blob %s, which is missing", path, e.Method, e.URL, e.Blob)
		}
		if _, ok := c.byKey[e.Key()]; !ok {
			c.byKey[e.Key()] = i
		}
		if _, ok := c.byURL[looseKey(e.Method, e.URL)]; !ok {
			c.byURL[looseKey(e.Method, e.URL)] = i
		}
	}
	return c, nil
}

// Close closes the tar.
func (c *Cache) Close() error { return c.f.Close() }

// Entries are the recorded rows.
func (c *Cache) Entries() []Entry { return c.entries }

// Lookup finds the recorded response for a request: the exact key first,
// then, for a request with a body, the same method and URL (a client that
// sends a nonce in a POST body still replays).
func (c *Cache) Lookup(method, u, bodySHA string) (Entry, *io.SectionReader, bool) {
	i, ok := c.byKey[requestKey(method, u, bodySHA)]
	if !ok && bodySHA != "" {
		i, ok = c.byURL[looseKey(method, u)]
	}
	if !ok {
		return Entry{}, nil, false
	}
	e := c.entries[i]
	b := c.blobs[e.Blob]
	return e, io.NewSectionReader(c.f, b.off, b.size), true
}
