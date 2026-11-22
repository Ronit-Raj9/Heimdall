package onboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/projectfs"
)

// Env is what the pipeline needs from the server: the org's project folder
// and the guarded ways out to the network.
type Env struct {
	Root          string // the org's project folder (holds uploads/, benchmarks/, drafts/)
	BenchmarksDir string // the resolved benchmarks root; empty: Root/benchmarks
	Hosted        bool
	Actor         string
	// Store, when set, is the org's project store: publish writes the
	// benchmark's suite.yaml through it (atomic, versioned).
	Store projectfs.Store

	// Fetch downloads a public https URL with the SSRF guard
	// (benchcatalog.Fetcher); nil uses a default fetcher.
	Fetch func(ctx context.Context, raw string) (name string, data []byte, err error)
	// Token reveals the secret a Connection names (server side only).
	Token func(ctx context.Context, connection string) (string, error)
	// HTTP calls the fixed public APIs (Hugging Face); nil: a client with
	// a 30 s timeout. HFAPI and HFHub are the bases (tests point them at a
	// fake server).
	HTTP  *http.Client
	HFAPI string
	HFHub string
}

// Default API bases.
const (
	DefaultHFAPI = "https://datasets-server.huggingface.co"
	DefaultHFHub = "https://huggingface.co"
)

// Benchmarks is the benchmarks root.
func (e *Env) Benchmarks() string {
	if e.BenchmarksDir != "" {
		return e.BenchmarksDir
	}
	return filepath.Join(e.Root, "benchmarks")
}

func (e *Env) fetch(ctx context.Context, raw string) (string, []byte, error) {
	if e.Fetch != nil {
		return e.Fetch(ctx, raw)
	}
	f := benchcatalog.NewFetcher()
	f.MaxBytes = MaxDataBytes
	return f.Fetch(ctx, raw)
}

func (e *Env) client() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (e *Env) hfAPI() string {
	if e.HFAPI != "" {
		return strings.TrimRight(e.HFAPI, "/")
	}
	return DefaultHFAPI
}

func (e *Env) hfHub() string {
	if e.HFHub != "" {
		return strings.TrimRight(e.HFHub, "/")
	}
	return DefaultHFHub
}

// token is the secret of in.Connection, or "".
func (e *Env) token(ctx context.Context, in Input) (string, error) {
	if in.Connection == "" {
		return "", nil
	}
	if e.Token == nil {
		return "", errors.New("saved connections are not available on this server")
	}
	return e.Token(ctx, in.Connection)
}

// Peek is what Detect sees of an input.
type Peek struct {
	Name string // file name, or the last URL segment
	Ext  string // lower-case extension with the dot
	// Data is the whole file when it is at most MaxDataBytes (rows, specs).
	Data []byte
	Size int64
	// Dir is the absolute folder of a folder upload (an extracted zip);
	// Files are its relative paths (at most maxPeekFiles).
	Dir   string
	Files []string
	// URL is the parsed URL input; Harbor ids have none.
	URL      *url.URL
	Raw      string // the URL input as given
	FetchErr string // why a URL could not be read, when it could not
}

const maxPeekFiles = 5000

// HasFile reports whether the folder holds a file with this base name.
func (p Peek) HasFile(base string) bool {
	for _, f := range p.Files {
		if path.Base(f) == base {
			return true
		}
	}
	return false
}

// Peek reads enough of in for Detect.
func (e *Env) Peek(ctx context.Context, in Input) (Peek, error) {
	switch {
	case strings.TrimSpace(in.Data) != "":
		name := in.Name
		if name == "" {
			name = "pasted.txt"
		}
		return Peek{Name: name, Ext: strings.ToLower(filepath.Ext(name)), Data: []byte(in.Data), Size: int64(len(in.Data))}, nil
	case strings.TrimSpace(in.Upload) != "":
		abs, err := e.UploadPath(in.Upload)
		if err != nil {
			return Peek{}, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return Peek{}, fmt.Errorf("upload %s not found; upload the file again", in.Upload)
		}
		p := Peek{Name: filepath.Base(abs), Ext: strings.ToLower(filepath.Ext(abs)), Size: st.Size()}
		if st.IsDir() {
			p.Dir, p.Ext = abs, ""
			p.Files = listFiles(abs, maxPeekFiles)
			return p, nil
		}
		if st.Size() <= MaxDataBytes {
			if p.Data, err = os.ReadFile(abs); err != nil {
				return Peek{}, err
			}
		}
		return p, nil
	}
	raw := strings.TrimSpace(in.URL)
	p := Peek{Raw: raw}
	if HarborID(raw) {
		p.Name = raw
		return p, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return p, errors.New("not a link: paste an https URL, a Harbor id such as terminal-bench@2.0, or drop a file")
	}
	p.URL = u
	p.Name = path.Base(u.Path)
	p.Ext = strings.ToLower(path.Ext(u.Path))
	if knownHost(u.Hostname()) {
		return p, nil // the source calls its own API
	}
	name, data, err := e.fetch(ctx, raw)
	if err != nil {
		p.FetchErr = err.Error()
		return p, nil
	}
	if name != "" {
		p.Name, p.Ext = name, strings.ToLower(filepath.Ext(name))
	}
	p.Data, p.Size = data, int64(len(data))
	return p, nil
}

func knownHost(h string) bool {
	h = strings.ToLower(h)
	return h == "huggingface.co" || h == "www.huggingface.co" || h == "hf.co" || h == "github.com" || h == "www.github.com"
}

var harborIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,80}@[A-Za-z0-9][A-Za-z0-9._-]{0,40}$`)

// HarborID reports whether s is a Harbor registry id (name@version).
func HarborID(s string) bool { return harborIDRE.MatchString(strings.TrimSpace(s)) }

// UploadPath confines rel to the project's uploads/ folder.
func (e *Env) UploadPath(rel string) (string, error) {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	rel = strings.TrimPrefix(rel, "uploads/")
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", errors.New("upload: a path under uploads/")
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("upload: a path under uploads/")
	}
	base := filepath.Join(e.Root, "uploads")
	abs := filepath.Join(base, filepath.FromSlash(clean))
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		rb, err := filepath.EvalSymlinks(base)
		if err != nil {
			rb = base
		}
		r, err := filepath.Rel(rb, real)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", errors.New("upload: a path under uploads/")
		}
		abs = real
	}
	return abs, nil
}

// listFiles lists regular files under dir, slash-separated, at most max.
func listFiles(dir string, max int) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); p != dir && (n == ".git" || n == "node_modules" || n == "__MACOSX") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= max {
			return io.EOF
		}
		if rel, err := filepath.Rel(dir, p); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}
