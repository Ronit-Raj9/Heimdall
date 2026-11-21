package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// Uploads area (web parity L24, B13): zip uploads from the browser or the CLI.
//
//	POST /uploads/runs     a zip of sealed experiment folders; every sealed run
//	                       is verified (tenancy.ImportRuns) before it is kept.
//	POST /uploads/project  a zip of project folders (agents/, benchmarks/,
//	                       experiments/, policies/) written through projectfs.
//	POST /uploads/agent    a zip of one agent folder, written to agents/<name>/.
//
// The body is the zip itself (Content-Type application/zip or
// application/octet-stream) or a multipart form with the zip in "file".
// Every entry name is checked before anything is written: an absolute path,
// "..", a symlink or a .git/.heimdall entry refuses the whole upload.

// Upload limits for runs zips. Sealed run folders hold many small files, so
// they get a larger entry budget than project zips (projectfs.MaxZipEntries).
const (
	maxRunsZipEntries = 50000
	maxRunsZipBytes   = 512 << 20 // uncompressed
	maxRunsFileBytes  = 128 << 20
)

// UploadedFile is one file an upload wrote or refused.
type UploadedFile struct {
	Area  string `json:"area"`
	Path  string `json:"path"`
	Size  int64  `json:"size,omitempty"`
	Error string `json:"error,omitempty"`
}

// UploadFilesReport is the answer of POST /uploads/project and /uploads/agent.
type UploadFilesReport struct {
	Written []UploadedFile `json:"written"`
	Refused []UploadedFile `json:"refused"`
	Skipped []string       `json:"skipped"` // OS clutter (__MACOSX, .DS_Store) and folders outside the project areas
}

// UploadRunsReport is the answer of POST /uploads/runs.
type UploadRunsReport struct {
	tenancy.ImportReport
}

var agentNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var projectAreas = map[string]projectfs.Area{
	"agents":      projectfs.AreaAgents,
	"benchmarks":  projectfs.AreaBenchmarks,
	"experiments": projectfs.AreaExperiments,
	"policies":    projectfs.AreaPolicies,
}

type uploadsHandler struct {
	mode      schemas.Mode
	storeRoot string // the org's evidence store root ("" when unknown)
	svc       Services
}

func init() {
	RegisterOrgArea("uploads", func(env OrgEnv, r *router.Router) {
		h := &uploadsHandler{mode: env.Mode, svc: env.Services}
		if env.Cfg != nil && env.Cfg.RunsRoot != "" {
			h.storeRoot = env.Cfg.Path(env.Cfg.RunsRoot)
		}
		r.POST(APIPrefix+"/uploads/runs", env.U(h.postRuns))
		r.POST(APIPrefix+"/uploads/project", env.U(h.postProject))
		r.POST(APIPrefix+"/uploads/agent", env.U(h.postAgent))
	})
}

// zipBody returns the uploaded zip: the multipart "file" part when the
// request is a form, the raw body otherwise.
func zipBody(ctx *fasthttp.RequestCtx) ([]byte, error) {
	ct := string(ctx.Request.Header.ContentType())
	mt, _, _ := mime.ParseMediaType(ct)
	if mt == "multipart/form-data" {
		fh, err := ctx.FormFile("file")
		if err != nil {
			return nil, errors.New(`multipart upload needs the zip in a form field named "file"`)
		}
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, lib.MaxUploadBytes+1))
	}
	body := ctx.PostBody()
	if len(body) == 0 {
		return nil, errors.New("empty body: send the zip as the request body (Content-Type application/zip)")
	}
	return body, nil
}

func (h *uploadsHandler) openZip(ctx *fasthttp.RequestCtx) (*zip.Reader, *bytes.Reader, bool) {
	data, err := zipBody(ctx)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return nil, nil, false
	}
	if len(data) > lib.MaxUploadBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge,
			fmt.Sprintf("upload is larger than %d MiB; split it into several zips", lib.MaxUploadBytes>>20))
		return nil, nil, false
	}
	br := bytes.NewReader(data)
	zr, err := zip.NewReader(br, int64(len(data)))
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "not a zip file: "+err.Error())
		return nil, nil, false
	}
	return zr, br, true
}

// zipEntry is one regular file of a checked zip.
type zipEntry struct {
	name string // cleaned slash path
	f    *zip.File
}

// errUnsafeZip marks an entry that would escape the destination (zip-slip),
// a link, or a reserved folder: the whole upload is refused.
var errUnsafeZip = errors.New("unsafe zip entry")

// cleanZipName returns the cleaned slash path of a zip entry, "" for an
// entry to skip silently (OS clutter), or errUnsafeZip.
func cleanZipName(name string) (string, error) {
	n := strings.ReplaceAll(name, "\\", "/")
	if n == "" || strings.HasPrefix(n, "/") || strings.ContainsRune(n, 0) || filepath.IsAbs(n) ||
		(len(n) >= 2 && n[1] == ':') {
		return "", fmt.Errorf("%w: %q is an absolute path", errUnsafeZip, name)
	}
	for _, part := range strings.Split(n, "/") {
		switch part {
		case "..":
			return "", fmt.Errorf("%w: %q leaves the upload folder", errUnsafeZip, name)
		case ".git", ".heimdall":
			return "", fmt.Errorf("%w: %q is inside a reserved folder", errUnsafeZip, name)
		case "__MACOSX":
			return "", nil
		}
	}
	c := path.Clean("/" + n)[1:]
	if base := path.Base(c); base == ".DS_Store" || base == "Thumbs.db" {
		return "", nil
	}
	return c, nil
}

// checkZip validates every entry name and the size budget before anything is
// written, and returns the regular files sorted by name.
func checkZip(zr *zip.Reader, maxEntries int, maxTotal, maxFile uint64) ([]zipEntry, []string, error) {
	if len(zr.File) > maxEntries {
		return nil, nil, fmt.Errorf("zip holds %d entries; at most %d", len(zr.File), maxEntries)
	}
	var out []zipEntry
	var skipped []string
	var total uint64
	seen := map[string]bool{}
	for _, f := range zr.File {
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 || (mode&os.ModeType != 0 && !mode.IsDir()) {
			return nil, nil, fmt.Errorf("%w: %q is a link or special file", errUnsafeZip, f.Name)
		}
		c, err := cleanZipName(f.Name)
		if err != nil {
			return nil, nil, err
		}
		if c == "" {
			skipped = append(skipped, f.Name)
			continue
		}
		if mode.IsDir() || strings.HasSuffix(f.Name, "/") {
			continue
		}
		if f.UncompressedSize64 > maxFile {
			return nil, nil, fmt.Errorf("%s is %d bytes; at most %d per file", c, f.UncompressedSize64, maxFile)
		}
		total += f.UncompressedSize64
		if total > maxTotal {
			return nil, nil, fmt.Errorf("zip unpacks to more than %d MiB", maxTotal>>20)
		}
		if seen[c] {
			return nil, nil, fmt.Errorf("zip lists %s twice", c)
		}
		seen[c] = true
		out = append(out, zipEntry{name: c, f: f})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, skipped, nil
}

// readEntry reads one entry, refusing more than limit bytes whatever the
// header claims (a forged size cannot inflate past it).
func readEntry(e zipEntry, limit int64) ([]byte, error) {
	rc, err := e.f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", e.name, limit)
	}
	return data, nil
}

// singleTop returns the one top-level folder every entry shares, or "".
func singleTop(entries []zipEntry) string {
	top := ""
	for _, e := range entries {
		i := strings.IndexByte(e.name, '/')
		if i < 0 {
			return ""
		}
		if top == "" {
			top = e.name[:i]
		} else if e.name[:i] != top {
			return ""
		}
	}
	return top
}

func stripTop(entries []zipEntry, top string) []zipEntry {
	if top == "" {
		return entries
	}
	out := make([]zipEntry, len(entries))
	for i, e := range entries {
		out[i] = zipEntry{name: strings.TrimPrefix(e.name, top+"/"), f: e.f}
	}
	return out
}

func actorOf(ctx *fasthttp.RequestCtx) string {
	p := auth.PrincipalFrom(ctx)
	switch {
	case p.Email != "":
		return p.Email
	case p.UserID != "":
		return p.UserID
	case p.TokenID != "":
		return "token:" + p.TokenID
	}
	return string(p.Kind)
}

func (h *uploadsHandler) zipError(ctx *fasthttp.RequestCtx, err error) {
	lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "zip refused, nothing was written: "+err.Error())
}

// postRuns handles POST /uploads/runs: unpack to a private temp folder, then
// tenancy.ImportRuns copies each experiment to staging, verifies every seal
// and the seals.log chain, and only then moves it into the org's store.
func (h *uploadsHandler) postRuns(ctx *fasthttp.RequestCtx) {
	if h.storeRoot == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"this server has no runs store configured (runs_root); runs cannot be imported")
		return
	}
	zr, _, ok := h.openZip(ctx)
	if !ok {
		return
	}
	entries, _, err := checkZip(zr, maxRunsZipEntries, maxRunsZipBytes, maxRunsFileBytes)
	if err != nil {
		h.zipError(ctx, err)
		return
	}
	tmp, err := os.MkdirTemp("", "heimdall-upload-runs-")
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "temp folder: "+err.Error())
		return
	}
	defer os.RemoveAll(tmp)
	for _, e := range entries {
		data, err := readEntry(e, maxRunsFileBytes)
		if err != nil {
			h.zipError(ctx, err)
			return
		}
		dst := filepath.Join(tmp, filepath.FromSlash(e.name))
		if rel, err := filepath.Rel(tmp, dst); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			h.zipError(ctx, fmt.Errorf("%w: %q", errUnsafeZip, e.name))
			return
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
			return
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
			return
		}
	}
	src := runsSource(tmp)
	if src == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"zip holds no experiment folder: zip the runs folder so each experiment folder (with its manifest.json) is at the top or one level down")
		return
	}
	rep, err := tenancy.ImportRuns(context.WithoutCancel(ctx), src, h.storeRoot, nil)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "import: "+err.Error())
		return
	}
	if rep.Imported > 0 {
		h.refreshIndex(ctx)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &UploadRunsReport{ImportReport: *rep})
}

// refreshIndex rebuilds the in-memory index when it can (local mode); the
// hosted runtime rebuilds on the generation bump ImportRuns made.
func (h *uploadsHandler) refreshIndex(ctx context.Context) {
	if r, ok := h.svc.Index.(interface{ Rebuild(context.Context) error }); ok {
		_ = r.Rebuild(context.WithoutCancel(ctx))
	}
}

// runsSource is the folder of tmp holding experiment folders: tmp itself, or
// its one subfolder (a zipped "runs/" folder). "" when neither has one.
func runsSource(tmp string) string {
	has := func(dir string) bool {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, e := range ents {
			if e.IsDir() {
				if fi, err := os.Stat(filepath.Join(dir, e.Name(), "manifest.json")); err == nil && fi.Mode().IsRegular() {
					return true
				}
			}
		}
		return false
	}
	if has(tmp) {
		return tmp
	}
	ents, err := os.ReadDir(tmp)
	if err != nil {
		return ""
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 && has(filepath.Join(tmp, dirs[0])) {
		return filepath.Join(tmp, dirs[0])
	}
	return ""
}

func (h *uploadsHandler) projects(ctx *fasthttp.RequestCtx) (projectfs.Store, bool) {
	if h.svc.Projects == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"project files are not available on this server: the project folder could not be opened")
		return nil, false
	}
	return h.svc.Projects, true
}

// postProject handles POST /uploads/project. The zip's top-level folders are
// project areas; a zip of one wrapping folder (myproject/agents/...) is
// unwrapped. Files outside the areas are skipped and listed.
func (h *uploadsHandler) postProject(ctx *fasthttp.RequestCtx) {
	ps, ok := h.projects(ctx)
	if !ok {
		return
	}
	zr, _, ok := h.openZip(ctx)
	if !ok {
		return
	}
	entries, skipped, err := checkZip(zr, projectfs.MaxZipEntries, projectfs.MaxZipBytes, projectfs.MaxFileBytes)
	if err != nil {
		h.zipError(ctx, err)
		return
	}
	if top := singleTop(entries); top != "" {
		if _, isArea := projectAreas[top]; !isArea {
			entries = stripTop(entries, top)
		}
	}
	type target struct {
		area projectfs.Area
		rel  string
		e    zipEntry
	}
	var targets []target
	for _, e := range entries {
		i := strings.IndexByte(e.name, '/')
		if i < 0 {
			skipped = append(skipped, e.name)
			continue
		}
		area, ok := projectAreas[e.name[:i]]
		if !ok {
			skipped = append(skipped, e.name)
			continue
		}
		rel := e.name[i+1:]
		ensureArea(ps, area)
		if _, err := ps.Resolve(area, rel); err != nil {
			h.zipError(ctx, fmt.Errorf("%s: %w", e.name, err))
			return
		}
		targets = append(targets, target{area: area, rel: rel, e: e})
	}
	if len(targets) == 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"zip has no files under agents/, benchmarks/, experiments/ or policies/; zip the project folder itself")
		return
	}
	rep := UploadFilesReport{Written: []UploadedFile{}, Refused: []UploadedFile{}, Skipped: nonNil(skipped)}
	opts := projectfs.WriteOpts{Actor: actorOf(ctx), Note: "upload: project zip"}
	for _, t := range targets {
		rep.add(ctx, ps, t.area, t.rel, t.e, opts)
	}
	h.sendFiles(ctx, &rep)
}

// postAgent handles POST /uploads/agent?name=<agent>. The files land in
// agents/<name>/. Without name, a zip of one folder names the agent after it.
func (h *uploadsHandler) postAgent(ctx *fasthttp.RequestCtx) {
	ps, ok := h.projects(ctx)
	if !ok {
		return
	}
	zr, raw, ok := h.openZip(ctx)
	if !ok {
		return
	}
	entries, skipped, err := checkZip(zr, projectfs.MaxZipEntries, projectfs.MaxZipBytes, projectfs.MaxFileBytes)
	if err != nil {
		h.zipError(ctx, err)
		return
	}
	name := strings.TrimSpace(string(ctx.QueryArgs().Peek("name")))
	top := singleTop(entries)
	if name == "" {
		name = top
	}
	if name == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"name the agent: pass ?name=<agent>, or zip one folder named after the agent")
		return
	}
	if !agentNamePattern.MatchString(name) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"agent name may use letters, digits, '.', '_' and '-' (at most 128, starting with a letter or digit)")
		return
	}
	if len(entries) == 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "zip holds no files")
		return
	}
	entries = stripTop(entries, top)
	ensureArea(ps, projectfs.AreaAgents)
	for _, e := range entries {
		if _, err := ps.Resolve(projectfs.AreaAgents, name+"/"+e.name); err != nil {
			h.zipError(ctx, fmt.Errorf("%s: %w", e.name, err))
			return
		}
	}
	opts := projectfs.WriteOpts{Actor: actorOf(ctx), Note: "upload: agent " + name}
	rep := UploadFilesReport{Written: []UploadedFile{}, Refused: []UploadedFile{}, Skipped: nonNil(skipped)}
	// projectfs.ExtractZip (L01) when it is implemented and the zip needs no
	// renaming; otherwise the same checks file by file through Write.
	if top == "" {
		got, err := ps.ExtractZip(context.WithoutCancel(ctx), projectfs.AreaAgents, name, raw, raw.Size(), opts)
		switch {
		case err == nil:
			for _, en := range got {
				if !en.Dir {
					rep.Written = append(rep.Written, UploadedFile{Area: string(projectfs.AreaAgents), Path: en.Path, Size: en.Size})
				}
			}
			h.sendFiles(ctx, &rep)
			return
		case !errors.Is(err, projectfs.ErrNotImplemented):
			h.zipError(ctx, err)
			return
		}
	}
	for _, e := range entries {
		rep.add(ctx, ps, projectfs.AreaAgents, name+"/"+e.name, e, opts)
	}
	h.sendFiles(ctx, &rep)
}

func (r *UploadFilesReport) add(ctx context.Context, ps projectfs.Store, a projectfs.Area, rel string, e zipEntry, o projectfs.WriteOpts) {
	data, err := readEntry(e, projectfs.MaxFileBytes)
	if err == nil {
		var en projectfs.Entry
		en, err = ps.Write(context.WithoutCancel(ctx), a, rel, data, o)
		if err == nil {
			r.Written = append(r.Written, UploadedFile{Area: string(a), Path: en.Path, Size: en.Size})
			return
		}
	}
	r.Refused = append(r.Refused, UploadedFile{Area: string(a), Path: rel, Error: err.Error()})
}

// sendFiles answers 200 when at least one file was written, 422 when every
// file was refused.
func (h *uploadsHandler) sendFiles(ctx *fasthttp.RequestCtx, rep *UploadFilesReport) {
	status := fasthttp.StatusOK
	if len(rep.Written) == 0 && len(rep.Refused) > 0 {
		status = fasthttp.StatusUnprocessableEntity
	}
	lib.SendJSON(ctx, h.mode, status, rep)
}

// ensureArea creates the area folder of the project when it is missing:
// projectfs resolves symlinks against the existing area folder, so a fresh
// project needs it before the first write.
func ensureArea(ps projectfs.Store, a projectfs.Area) {
	if root := ps.Root(); root != "" {
		_ = os.MkdirAll(filepath.Join(root, string(a)), 0o755)
	}
}
