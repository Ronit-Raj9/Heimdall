package main

// L26 CLI push/pull: sync a local project folder (agents/, benchmarks/,
// experiments/, policies/, uploads/) with a Heimdall server.
//
//	bifrost-bench pull -server https://heimdall.example.com -token hm_...
//	bifrost-bench push -server https://heimdall.example.com -token hm_...
//
// Both read GET /project/manifest (sha256 per file) first. Pull downloads
// /project/export.zip and writes only the files whose hash differs. Push
// sends each changed file with PUT /project/files/{area}/{path} (If-Match on
// the server's hash, so a file someone edited on the web is not silently
// overwritten); files over the 1 MiB per-file limit go up as one zip per area
// through POST /uploads/project.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/projectfs"
)

func init() {
	register("push", "upload changed project files to a Heimdall server: push -server URL -token TOKEN [-dir .] [-delete] [-force] [-dry-run]", runPush)
	register("pull", "download the server's project files: pull -server URL [-token TOKEN] [-dir .] [-delete] [-dry-run]", runPull)
}

const (
	syncTokenEnv  = "HEIMDALL_TOKEN"
	syncServerEnv = "HEIMDALL_SERVER"
	// maxPullExportBytes caps the export zip a pull downloads.
	maxPullExportBytes = 1 << 30
	// maxSyncFileBytes caps one file a pull writes.
	maxSyncFileBytes = 256 << 20
)

// syncOpts are the flags push and pull share.
type syncOpts struct {
	server, token, org, dir string
	del, force, dryRun      bool
	timeout                 time.Duration
}

func syncFlags(name string, args []string) (*syncOpts, int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	o := &syncOpts{}
	fs.StringVar(&o.server, "server", os.Getenv(syncServerEnv), "Heimdall server URL, e.g. https://heimdall.example.com (env "+syncServerEnv+")")
	fs.StringVar(&o.token, "token", "", "API token (hm_...) or local write token (env "+syncTokenEnv+", then "+"BENCH_WRITE_TOKEN)")
	fs.StringVar(&o.org, "org", "", "org slug or id, when the token's user belongs to several orgs")
	fs.StringVar(&o.dir, "dir", ".", "local project folder that holds agents/, benchmarks/, experiments/, policies/, uploads/")
	fs.BoolVar(&o.del, "delete", false, "also delete files that exist only on the destination side")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print what would change and change nothing")
	fs.DurationVar(&o.timeout, "timeout", 10*time.Minute, "overall time limit")
	if name == "push" {
		fs.BoolVar(&o.force, "force", false, "overwrite server files even when they changed since the last sync")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, exitOK
		}
		return nil, exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "%s %s: unexpected argument %q\n", progName, name, fs.Arg(0))
		return nil, exitUsage
	}
	if o.token == "" {
		o.token = os.Getenv(syncTokenEnv)
	}
	if o.token == "" {
		o.token = os.Getenv("BENCH_WRITE_TOKEN")
	}
	if strings.TrimSpace(o.server) == "" {
		fmt.Fprintf(os.Stderr, "%s %s: -server is required (or set %s), e.g. -server http://127.0.0.1:8090\n", progName, name, syncServerEnv)
		return nil, exitUsage
	}
	if name == "push" && o.token == "" {
		fmt.Fprintf(os.Stderr, "%s push: a token is required: pass -token or set %s (create one under Settings, API tokens)\n", progName, syncTokenEnv)
		return nil, exitUsage
	}
	return o, -1
}

// syncClient talks to one server's bench API.
type syncClient struct {
	base, token, org string
	hc               *http.Client
}

func newSyncClient(o *syncOpts) (*syncClient, error) {
	u, err := url.Parse(strings.TrimSpace(o.server))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("-server %q is not an http(s) URL", o.server)
	}
	base := strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/")
	base = strings.TrimSuffix(base, handlers.APIPrefix)
	return &syncClient{base: base + handlers.APIPrefix, token: o.token, org: o.org, hc: &http.Client{}}, nil
}

func (c *syncClient) do(ctx context.Context, method, p string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+p, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.org != "" {
		req.Header.Set("X-Heimdall-Org", c.org)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return c.hc.Do(req)
}

// apiError turns a non-2xx response into an error with the server's message.
func apiError(method, p string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	hint := ""
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		hint = " (check -token)"
	case http.StatusForbidden:
		hint = " (your role cannot do this; push needs member or above)"
	case http.StatusNotFound:
		hint = " (is -server a Heimdall server with project sync?)"
	}
	return fmt.Errorf("%s %s: %d %s%s", method, p, resp.StatusCode, msg, hint)
}

func (c *syncClient) manifest(ctx context.Context) (map[string]handlers.ManifestFile, error) {
	p := strings.TrimPrefix(handlers.ProjectManifestPath, handlers.APIPrefix)
	resp, err := c.do(ctx, http.MethodGet, p, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError("GET", p, resp)
	}
	var m handlers.ProjectManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	out := make(map[string]handlers.ManifestFile, len(m.Files))
	for _, f := range m.Files {
		if !syncArea(f.Area) || !safeRel(f.Path) {
			continue
		}
		out[f.Area+"/"+f.Path] = f
	}
	return out, nil
}

func syncArea(a string) bool {
	for _, k := range handlers.ProjectAreas {
		if string(k) == a {
			return true
		}
	}
	return false
}

// safeRel accepts a clean, relative, slash path with no "..", .git or .heimdall.
func safeRel(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") || path.Clean(p) != p {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." || part == ".git" || part == ".heimdall" {
			return false
		}
	}
	return true
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// localManifest hashes the regular files under dir/<area>. Symlinks, .git
// and .heimdall are skipped.
func localManifest(dir string) (map[string]handlers.ManifestFile, error) {
	out := map[string]handlers.ManifestFile{}
	for _, a := range handlers.ProjectAreas {
		base := filepath.Join(dir, string(a))
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == base && errors.Is(err, fs.ErrNotExist) {
					return filepath.SkipDir
				}
				return err
			}
			if d.IsDir() {
				if p != base && (d.Name() == ".git" || d.Name() == ".heimdall") {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !safeRel(rel) {
				return nil
			}
			sum, size, err := hashFile(p)
			if err != nil {
				return err
			}
			out[string(a)+"/"+rel] = handlers.ManifestFile{Area: string(a), Path: rel, Size: size, SHA256: sum}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return out, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// syncState is the last synced manifest per server, kept in
// <dir>/.heimdall/sync.json, so push can tell a file edited on the server
// since the last sync (a conflict) from one only edited locally.
type syncState map[string]map[string]string // server base -> key -> sha256

func syncStatePath(dir string) string { return filepath.Join(dir, ".heimdall", "sync.json") }

func loadSyncBase(dir, server string) map[string]string {
	var st syncState
	raw, err := os.ReadFile(syncStatePath(dir))
	if err != nil || json.Unmarshal(raw, &st) != nil {
		return map[string]string{}
	}
	if b := st[server]; b != nil {
		return b
	}
	return map[string]string{}
}

func saveSyncBase(dir, server string, m map[string]handlers.ManifestFile) error {
	st := syncState{}
	if raw, err := os.ReadFile(syncStatePath(dir)); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	b := make(map[string]string, len(m))
	for k, f := range m {
		b[k] = f.SHA256
	}
	st[server] = b
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(syncStatePath(dir)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(syncStatePath(dir), raw, 0o644)
}

func sortedKeys(m map[string]handlers.ManifestFile) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func runPush(args []string) int {
	o, code := syncFlags("push", args)
	if o == nil {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	st, err := pushProject(ctx, o, os.Stdout)
	if err != nil {
		return fail("push: %v", err)
	}
	if st.conflicts > 0 {
		fmt.Fprintf(os.Stderr, "push: %d file(s) changed on the server since your last pull; run `%s pull` first, or push -force to overwrite\n", st.conflicts, progName)
		return exitInfra
	}
	return exitOK
}

type syncStats struct{ put, deleted, unchanged, conflicts, zipped int }

func pushProject(ctx context.Context, o *syncOpts, out io.Writer) (syncStats, error) {
	var st syncStats
	c, err := newSyncClient(o)
	if err != nil {
		return st, err
	}
	local, err := localManifest(o.dir)
	if err != nil {
		return st, fmt.Errorf("read local project %s: %w", o.dir, err)
	}
	if len(local) == 0 && !o.del {
		return st, fmt.Errorf("no project files under %s (expected agents/, benchmarks/, experiments/, policies/ or uploads/); pass -dir", o.dir)
	}
	remote, err := c.manifest(ctx)
	if err != nil {
		return st, err
	}
	base := loadSyncBase(o.dir, c.base)
	big := map[string][]handlers.ManifestFile{} // area -> files over the PUT limit
	for _, k := range sortedKeys(local) {
		lf := local[k]
		rf, onServer := remote[k]
		if onServer && rf.SHA256 == lf.SHA256 {
			st.unchanged++
			continue
		}
		if onServer && !o.force && base[k] != rf.SHA256 {
			fmt.Fprintf(out, "conflict %s (changed on the server since your last sync)\n", k)
			st.conflicts++
			continue
		}
		if lf.Size > projectfs.MaxFileBytes {
			big[lf.Area] = append(big[lf.Area], lf)
			fmt.Fprintf(out, "zip  %s\n", k)
			continue
		}
		fmt.Fprintf(out, "put  %s\n", k)
		if o.dryRun {
			st.put++
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.dir, lf.Area, filepath.FromSlash(lf.Path)))
		if err != nil {
			return st, err
		}
		hdr := map[string]string{"Content-Type": "application/octet-stream"}
		if onServer && !o.force {
			hdr["If-Match"] = rf.SHA256
		}
		p := "/project/files/" + lf.Area + "/" + escapePath(lf.Path)
		resp, err := c.do(ctx, http.MethodPut, p, bytes.NewReader(data), hdr)
		if err != nil {
			return st, err
		}
		if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusPreconditionFailed {
			resp.Body.Close()
			fmt.Fprintf(out, "conflict %s (changed on the server)\n", k)
			st.conflicts++
			continue
		}
		if resp.StatusCode/100 != 2 {
			err := apiError("PUT", p, resp)
			resp.Body.Close()
			return st, err
		}
		resp.Body.Close()
		st.put++
	}
	for _, a := range handlers.ProjectAreas {
		files := big[string(a)]
		if len(files) == 0 {
			continue
		}
		st.zipped += len(files)
		if o.dryRun {
			continue
		}
		if err := c.uploadZip(ctx, o.dir, string(a), files); err != nil {
			return st, err
		}
	}
	if o.del {
		for _, k := range sortedKeys(remote) {
			if _, ok := local[k]; ok {
				continue
			}
			rf := remote[k]
			if !o.force && base[k] != rf.SHA256 {
				fmt.Fprintf(out, "conflict %s (not deleting: changed on the server since your last sync)\n", k)
				st.conflicts++
				continue
			}
			fmt.Fprintf(out, "del  %s\n", k)
			if o.dryRun {
				st.deleted++
				continue
			}
			hdr := map[string]string{}
			if !o.force {
				hdr["If-Match"] = rf.SHA256
			}
			p := "/project/files/" + rf.Area + "/" + escapePath(rf.Path)
			resp, err := c.do(ctx, http.MethodDelete, p, nil, hdr)
			if err != nil {
				return st, err
			}
			if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusPreconditionFailed {
				resp.Body.Close()
				fmt.Fprintf(out, "conflict %s (changed on the server)\n", k)
				st.conflicts++
				continue
			}
			if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
				err := apiError("DELETE", p, resp)
				resp.Body.Close()
				return st, err
			}
			resp.Body.Close()
			st.deleted++
		}
	}
	verb := "pushed"
	if o.dryRun {
		verb = "would push"
	} else if st.conflicts == 0 {
		if after, err := c.manifest(ctx); err == nil {
			_ = saveSyncBase(o.dir, c.base, after)
		}
	}
	fmt.Fprintf(out, "%s %d file(s), %d via zip, deleted %d, %d unchanged\n", verb, st.put, st.zipped, st.deleted, st.unchanged)
	return st, nil
}

// uploadZip sends files (area-relative) as one zip to POST /uploads/project.
// The zip holds area-relative paths; area and dir go both as query and form
// fields.
func (c *syncClient) uploadZip(ctx context.Context, dir, area string, files []handlers.ManifestFile) error {
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for _, f := range files {
		w, err := zw.Create(f.Path)
		if err != nil {
			return err
		}
		src, err := os.Open(filepath.Join(dir, area, filepath.FromSlash(f.Path)))
		if err != nil {
			return err
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if zbuf.Len() > projectfs.MaxZipBytes {
		return fmt.Errorf("%s: large files total %d bytes, over the %d byte upload limit; split them", area, zbuf.Len(), projectfs.MaxZipBytes)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("area", area)
	_ = mw.WriteField("dir", "")
	fw, err := mw.CreateFormFile("file", area+".zip")
	if err != nil {
		return err
	}
	if _, err := fw.Write(zbuf.Bytes()); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	p := "/uploads/project?area=" + url.QueryEscape(area)
	resp, err := c.do(ctx, http.MethodPost, p, &body, map[string]string{"Content-Type": mw.FormDataContentType()})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return apiError("POST", p, resp)
	}
	return nil
}

func runPull(args []string) int {
	o, code := syncFlags("pull", args)
	if o == nil {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	if _, err := pullProject(ctx, o, os.Stdout); err != nil {
		return fail("pull: %v", err)
	}
	return exitOK
}

func pullProject(ctx context.Context, o *syncOpts, out io.Writer) (syncStats, error) {
	var st syncStats
	c, err := newSyncClient(o)
	if err != nil {
		return st, err
	}
	remote, err := c.manifest(ctx)
	if err != nil {
		return st, err
	}
	local, err := localManifest(o.dir)
	if err != nil {
		return st, fmt.Errorf("read local project %s: %w", o.dir, err)
	}
	need := map[string]handlers.ManifestFile{}
	for _, k := range sortedKeys(remote) {
		if lf, ok := local[k]; ok && lf.SHA256 == remote[k].SHA256 {
			st.unchanged++
			continue
		}
		need[k] = remote[k]
		fmt.Fprintf(out, "get  %s\n", k)
	}
	if len(need) > 0 && !o.dryRun {
		if err := c.pullExport(ctx, o.dir, need); err != nil {
			return st, err
		}
	}
	st.put = len(need)
	if o.del {
		for _, k := range sortedKeys(local) {
			if _, ok := remote[k]; ok {
				continue
			}
			fmt.Fprintf(out, "del  %s\n", k)
			st.deleted++
			if o.dryRun {
				continue
			}
			lf := local[k]
			if err := os.Remove(filepath.Join(o.dir, lf.Area, filepath.FromSlash(lf.Path))); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return st, err
			}
		}
	}
	verb := "pulled"
	if o.dryRun {
		verb = "would pull"
	} else if err := saveSyncBase(o.dir, c.base, remote); err != nil {
		return st, fmt.Errorf("save sync state: %w", err)
	}
	fmt.Fprintf(out, "%s %d file(s), deleted %d, %d unchanged\n", verb, st.put, st.deleted, st.unchanged)
	return st, nil
}

// pullExport downloads /project/export.zip and writes the needed files whose
// content matches the manifest hash. Entries are "<area>/<path>"; a single
// leading folder (e.g. "project/agents/x.yaml") is tolerated.
func (c *syncClient) pullExport(ctx context.Context, dir string, need map[string]handlers.ManifestFile) error {
	resp, err := c.do(ctx, http.MethodGet, "/project/export.zip", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError("GET", "/project/export.zip", resp)
	}
	tmp, err := os.CreateTemp("", "heimdall-pull-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxPullExportBytes+1))
	if err != nil {
		return fmt.Errorf("download export: %w", err)
	}
	if n > maxPullExportBytes {
		return fmt.Errorf("export zip is over %d bytes", maxPullExportBytes)
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		return fmt.Errorf("read export zip: %w", err)
	}
	got := map[string]bool{}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || !zf.Mode().IsRegular() {
			continue
		}
		key := exportKey(zf.Name)
		want, ok := need[key]
		if !ok || got[key] {
			continue
		}
		if zf.UncompressedSize64 > maxSyncFileBytes {
			return fmt.Errorf("%s: over the %d byte per-file limit", key, maxSyncFileBytes)
		}
		if err := writeZipEntry(dir, want, zf); err != nil {
			return err
		}
		got[key] = true
	}
	var missing []string
	for k := range need {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("export zip lacks %d file(s) the manifest lists, e.g. %s; retry the pull", len(missing), missing[0])
	}
	return nil
}

// exportKey maps a zip entry name to "<area>/<path>", or "" when it is not
// a safe project file.
func exportKey(name string) string {
	name = strings.TrimPrefix(name, "./")
	if !safeRel(name) {
		return ""
	}
	parts := strings.SplitN(name, "/", 3)
	if len(parts) >= 2 && syncArea(parts[0]) {
		return name
	}
	if len(parts) == 3 && syncArea(parts[1]) {
		return parts[1] + "/" + parts[2]
	}
	return ""
}

// writeZipEntry writes one entry atomically under dir/<area>/<path> after
// checking its sha256 against the manifest. The target is confined to dir
// and never follows a symlink out of it.
func writeZipEntry(dir string, want handlers.ManifestFile, zf *zip.File) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	target := filepath.Join(root, want.Area, filepath.FromSlash(want.Path))
	if !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return fmt.Errorf("%s/%s: outside %s", want.Area, want.Path, dir)
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if realParent, err := filepath.EvalSymlinks(parent); err == nil {
		realRoot, rerr := filepath.EvalSymlinks(root)
		if rerr != nil {
			realRoot = root
		}
		if realParent != realRoot && !strings.HasPrefix(realParent, realRoot+string(os.PathSeparator)) {
			return fmt.Errorf("%s/%s: folder is a symlink out of %s", want.Area, want.Path, dir)
		}
	}
	if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s/%s: local file is a symlink; remove it and pull again", want.Area, want.Path)
	}
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(parent, ".pull-*")
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(rc, maxSyncFileBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
		err = fmt.Errorf("%s/%s: content in the export does not match the manifest hash (changed during pull?); retry", want.Area, want.Path)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), target)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
