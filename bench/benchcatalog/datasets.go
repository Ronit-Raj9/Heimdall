package benchcatalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/projectfs"
)

// Dataset upload and server-side fetch for importer entries (gap G11). A
// hosted user cannot place files on the server, so the catalog takes the
// upstream data either as an upload or as a public https URL. Both land in
// the org's uploads area under datasets/<entry>/, and the import job reads
// that path.

// DataSpec says which data option works for an importer and what it expects.
type DataSpec struct {
	// Upload describes the file to upload (zip or the importer's own file).
	Upload string
	// Fetch is true when the data is public and a URL fetch can work.
	Fetch bool
	// FetchURL is a known public URL to prefill (empty when none is known).
	FetchURL string
	// Note is an extra step the user must take (e.g. a setting to pass).
	Note string
}

const repoZipRef = "A zip of a plain archive has no git history: set ref=<40-hex upstream commit> in the import settings."

// importerData is the data each importer reads.
var importerData = map[string]DataSpec{
	"harbor":          {Upload: "zip of Harbor task folders (each with task.toml), e.g. a GitHub archive of the dataset repo", Fetch: true},
	"swebench":        {Upload: "JSONL file (one SWE-bench row per line), or a zip of .jsonl files. Hugging Face serves parquet: export a split to JSONL first", Fetch: true},
	"impossiblebench": {Upload: "JSONL file (one ImpossibleBench row per line), or a zip of .jsonl files. Hugging Face serves parquet: export a split to JSONL first", Fetch: true},
	"longmemeval":     {Upload: "longmemeval_oracle.json (or longmemeval_s_cleaned.json)", Fetch: true, FetchURL: "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/main/longmemeval_oracle.json"},
	"locomo":          {Upload: "locomo10.json", Fetch: true, FetchURL: "https://raw.githubusercontent.com/snap-research/locomo/main/data/locomo10.json"},
	"gaia":            {Upload: "zip of 2023/validation (metadata.jsonl plus attachments). The dataset is gated, so URL fetch cannot work: accept the terms on Hugging Face, download, then upload"},
	"browsecomp-plus": {Upload: "queries.jsonl (convert the parquet with importers/browsecompplus/assets/tojsonl.py first)", Fetch: true},
	"tau2":            {Upload: "zip of the tau2-bench repo", Fetch: true, FetchURL: "https://github.com/sierra-research/tau2-bench/archive/refs/heads/main.zip", Note: repoZipRef},
	"agentdojo":       {Upload: "zip of the agentdojo repo", Fetch: true, FetchURL: "https://github.com/ethz-spylab/agentdojo/archive/refs/heads/main.zip", Note: repoZipRef},
	"mcpmark":         {Upload: "zip of the mcpmark repo with test_environments unzipped inside", Fetch: false, Note: repoZipRef},
	"marble":          {Upload: "zip of the MARBLE repo (multiagentbench/<scenario>/<scenario>_main.jsonl)", Fetch: true, FetchURL: "https://github.com/ulab-uiuc/MARBLE/archive/refs/heads/main.zip", Note: repoZipRef},
	"kernelbench":     {Upload: "zip of the KernelBench repo or its KernelBench/level1 folder", Fetch: true, FetchURL: "https://github.com/ScalingIntelligence/KernelBench/archive/refs/heads/main.zip"},
}

// recipeFetch are known public URLs of the headline benchmarks.
var recipeFetch = map[string]string{
	"terminal-bench-2": "https://github.com/laude-institute/terminal-bench-2/archive/refs/heads/main.zip",
	"skillsbench":      "https://github.com/benchflow-ai/skillsbench/archive/refs/tags/v1.0.zip",
}

// DataFor is the data spec of an importer entry.
func DataFor(entryID, importer string) DataSpec {
	d, ok := importerData[importer]
	if !ok {
		d = DataSpec{Upload: "zip of the upstream data, or the single data file the " + importer + " importer reads", Fetch: true}
	}
	if u, ok := recipeFetch[entryID]; ok {
		d.Fetch, d.FetchURL = true, u
	}
	return d
}

// applyData fills the data fields of an importer entry.
func applyData(e *Entry) {
	d := DataFor(e.ID, e.Importer)
	e.UploadExpects, e.FetchAllowed, e.FetchURL, e.DataNote = d.Upload, d.Fetch, d.FetchURL, d.Note
	e.DatasetPath = "uploads/" + DatasetDir(e.ID)
	if e.Available {
		h := "Upload " + d.Upload + "."
		if d.Fetch {
			h += " Or fetch it from a public https URL."
		}
		if d.Note != "" {
			h += " " + d.Note
		}
		e.Hint = h
	}
}

// DatasetDir is the uploads-area folder of an entry's data.
func DatasetDir(entryID string) string { return path.Join("datasets", entryID) }

// IsZip reports whether data looks like a zip archive.
func IsZip(name string, data []byte) bool {
	return bytes.HasPrefix(data, []byte("PK\x03\x04")) || strings.EqualFold(path.Ext(name), ".zip")
}

var safeFileRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// SafeFileName reduces name to one safe path segment.
func SafeFileName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Trim(safeFileRe.ReplaceAllString(name, "_"), "._")
	if name == "" {
		name = "data"
	}
	if len(name) > 100 {
		name = name[len(name)-100:]
	}
	return name
}

// SaveDataset replaces uploads/datasets/<entry>/ with data: a zip is
// extracted, any other file is stored as is. It returns the project-relative
// path the importer should read (the zip's single top folder when it has one).
func SaveDataset(ctx context.Context, store projectfs.Store, entryID, fileName string, data []byte, actor string) (string, error) {
	if store == nil {
		return "", errors.New("project files are not available on this server")
	}
	if len(data) == 0 {
		return "", errors.New("the dataset file is empty")
	}
	if len(data) > projectfs.MaxUploadFileBytes {
		return "", fmt.Errorf("%w: dataset is larger than %d MiB", projectfs.ErrTooLarge, projectfs.MaxUploadFileBytes>>20)
	}
	dir := DatasetDir(entryID)
	if err := store.Delete(ctx, projectfs.AreaUploads, dir, projectfs.WriteOpts{Actor: actor}); err != nil && !errors.Is(err, projectfs.ErrNotFound) {
		return "", fmt.Errorf("clear old dataset: %w", err)
	}
	o := projectfs.WriteOpts{Actor: actor, Note: "dataset for " + entryID}
	if IsZip(fileName, data) {
		ents, err := store.ExtractZip(ctx, projectfs.AreaUploads, dir, bytes.NewReader(data), int64(len(data)), o)
		if err != nil {
			return "", err
		}
		if len(ents) == 0 {
			return "", errors.New("the zip has no files")
		}
		return "uploads/" + zipTop(dir, ents), nil
	}
	name := SafeFileName(fileName)
	if _, err := store.Write(ctx, projectfs.AreaUploads, path.Join(dir, name), data, o); err != nil {
		return "", err
	}
	return "uploads/" + path.Join(dir, name), nil
}

// zipTop is dir/<top> when every extracted entry sits under one top folder.
func zipTop(dir string, ents []projectfs.Entry) string {
	top := ""
	for _, e := range ents {
		rel := strings.TrimPrefix(strings.TrimPrefix(e.Path, dir), "/")
		first, rest, ok := strings.Cut(rel, "/")
		if !ok || rest == "" || (top != "" && first != top) {
			return dir
		}
		top = first
	}
	return path.Join(dir, top)
}

// Fetch limits.
const (
	FetchTimeout     = 3 * time.Minute
	MaxFetchRedirect = 5
)

// Fetcher downloads public data over https with an SSRF guard: https only,
// no localhost, private, loopback or link-local addresses (checked again at
// dial time, so DNS cannot point back inside), a size cap and a timeout.
type Fetcher struct {
	MaxBytes int64
	Timeout  time.Duration
	// allowPrivate is for tests only (httptest servers listen on loopback).
	allowPrivate bool
	client       *http.Client
}

// NewFetcher is the default fetcher (size cap = the uploads per-file cap).
func NewFetcher() *Fetcher {
	return &Fetcher{MaxBytes: projectfs.MaxUploadFileBytes, Timeout: FetchTimeout}
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() ||
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64) // 100.64/10 carrier NAT
}

// ErrFetchRefused marks a URL the guard refuses.
var ErrFetchRefused = errors.New("url refused")

// CheckURL accepts only a public https URL.
func (f *Fetcher) CheckURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return nil, fmt.Errorf("%w: give an https URL of at most 2048 bytes", ErrFetchRefused)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: not an absolute URL", ErrFetchRefused)
	}
	if u.Scheme != "https" && !(f.allowPrivate && u.Scheme == "http") {
		return nil, fmt.Errorf("%w: only https URLs can be fetched", ErrFetchRefused)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: credentials in the URL are not allowed; only public data can be fetched", ErrFetchRefused)
	}
	if f.allowPrivate {
		return u, nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") {
		return nil, fmt.Errorf("%w: local host names cannot be fetched", ErrFetchRefused)
	}
	if ip := net.ParseIP(host); ip != nil && blockedIP(ip) {
		return nil, fmt.Errorf("%w: private or local addresses cannot be fetched", ErrFetchRefused)
	}
	if p := u.Port(); p != "" && p != "443" {
		return nil, fmt.Errorf("%w: only the default https port is allowed", ErrFetchRefused)
	}
	return u, nil
}

func (f *Fetcher) httpClient() *http.Client {
	if f.client != nil {
		return f.client
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !f.allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || blockedIP(ip) {
				return fmt.Errorf("%w: %s resolves to a private or local address", ErrFetchRefused, host)
			}
			return nil
		}
	}
	tr := &http.Transport{Proxy: nil, DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, MaxIdleConns: 2}
	f.client = &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= MaxFetchRedirect {
			return fmt.Errorf("%w: more than %d redirects", ErrFetchRefused, MaxFetchRedirect)
		}
		_, err := f.CheckURL(req.URL.String())
		return err
	}}
	return f.client
}

// Fetch downloads raw and returns a file name and the body.
func (f *Fetcher) Fetch(ctx context.Context, raw string) (string, []byte, error) {
	u, err := f.CheckURL(raw)
	if err != nil {
		return "", nil, err
	}
	max := f.MaxBytes
	if max <= 0 {
		max = projectfs.MaxUploadFileBytes
	}
	to := f.Timeout
	if to <= 0 {
		to = FetchTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "heimdall-bench-dataset-fetch")
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("fetch %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("fetch %s: HTTP %d (is the data public? gated datasets must be downloaded and uploaded)", u.Redacted(), resp.StatusCode)
	}
	if resp.ContentLength > max {
		return "", nil, fmt.Errorf("%w: %s is %d MiB, the limit is %d MiB", projectfs.ErrTooLarge, u.Redacted(), resp.ContentLength>>20, max>>20)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return "", nil, fmt.Errorf("fetch %s: %w", u.Redacted(), err)
	}
	if int64(len(data)) > max {
		return "", nil, fmt.Errorf("%w: %s is larger than %d MiB", projectfs.ErrTooLarge, u.Redacted(), max>>20)
	}
	name := path.Base(resp.Request.URL.Path)
	if IsZip("", data) && !strings.EqualFold(filepath.Ext(name), ".zip") {
		name += ".zip"
	}
	return SafeFileName(name), data, nil
}

// FetchDataset downloads raw with f and saves it as the entry's dataset. It
// returns the project-relative path the importer should read.
func FetchDataset(ctx context.Context, f *Fetcher, store projectfs.Store, entryID, raw, actor string) (string, error) {
	if f == nil {
		f = NewFetcher()
	}
	name, data, err := f.Fetch(ctx, raw)
	if err != nil {
		return "", err
	}
	return SaveDataset(ctx, store, entryID, name, data, actor)
}
