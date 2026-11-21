package handlers

// Project files area (web parity L01): read, edit, version and export the
// org's project folder (agents, benchmarks, experiments, policies, uploads)
// through projectfs. Reads are viewer routes; PUT and DELETE are member
// routes (tenancy/policy_projectfiles.go) and go through the write
// middleware.

import (
	"archive/zip"
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// ProjectAreas are the project folders the files area serves, in display order.
var ProjectAreas = []projectfs.Area{projectfs.AreaAgents, projectfs.AreaBenchmarks, projectfs.AreaExperiments, projectfs.AreaPolicies, projectfs.AreaUploads}

// ProjectFileList is GET /project/files and GET /project/files/{area}/{path} on a folder.
type ProjectFileList struct {
	Area    string            `json:"area,omitempty"`
	Dir     string            `json:"dir"`
	Entries []projectfs.Entry `json:"entries"`
}

// ProjectFile is GET /project/files/{area}/{path} on a file. Encoding is
// "utf-8" for text and "base64" for binary content.
type ProjectFile struct {
	Area     string          `json:"area"`
	Path     string          `json:"path"`
	Entry    projectfs.Entry `json:"entry"`
	Encoding string          `json:"encoding"`
	Content  string          `json:"content"`
}

// ProjectFileSaved is the answer to PUT and DELETE /project/files/{area}/{path}.
type ProjectFileSaved struct {
	Area    string           `json:"area"`
	Path    string           `json:"path"`
	Entry   *projectfs.Entry `json:"entry,omitempty"`
	Deleted bool             `json:"deleted,omitempty"`
}

// ProjectFileVersions is GET /project/versions/{area}/{path}.
type ProjectFileVersions struct {
	Area     string              `json:"area"`
	Path     string              `json:"path"`
	Versions []projectfs.Version `json:"versions"`
}

// maxExportBytes bounds GET /project/export.zip (uncompressed input).
const maxExportBytes = 512 << 20

type projectFilesHandler struct {
	mode  schemas.Mode
	store projectfs.Store
}

func init() {
	RegisterOrgArea("projectfiles", func(env OrgEnv, r *router.Router) {
		h := &projectFilesHandler{mode: env.Mode, store: env.Services.Projects}
		r.GET(APIPrefix+"/project/files", env.R(h.list))
		r.GET(APIPrefix+"/project/files/{area}/{path:*}", env.R(h.get))
		r.PUT(APIPrefix+"/project/files/{area}/{path:*}", env.W(h.put))
		r.DELETE(APIPrefix+"/project/files/{area}/{path:*}", env.W(h.del))
		r.GET(APIPrefix+"/project/versions/{area}/{path:*}", env.R(h.versions))
		r.GET(APIPrefix+"/project/export.zip", env.R(h.export))
	})
}

func (h *projectFilesHandler) ready(ctx *fasthttp.RequestCtx) bool {
	if h.store == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"project files are not available on this server: start it with a project folder (bifrost-bench serve -project <dir>)")
		return false
	}
	return true
}

// parseArea checks an area name.
func parseArea(raw string) (projectfs.Area, bool) {
	for _, a := range ProjectAreas {
		if string(a) == raw {
			return a, true
		}
	}
	return "", false
}

func areaError(raw string) string {
	names := make([]string, len(ProjectAreas))
	for i, a := range ProjectAreas {
		names[i] = string(a)
	}
	return fmt.Sprintf("unknown area %q: use one of %s", raw, strings.Join(names, ", "))
}

// pathParams reads {area} and {path}.
func (h *projectFilesHandler) pathParams(ctx *fasthttp.RequestCtx) (projectfs.Area, string, bool) {
	rawArea, _ := ctx.UserValue("area").(string)
	a, ok := parseArea(rawArea)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, areaError(rawArea))
		return "", "", false
	}
	rel, _ := ctx.UserValue("path").(string)
	return a, strings.TrimPrefix(rel, "/"), true
}

// sendStoreError maps projectfs errors to statuses with a next step.
func (h *projectFilesHandler) sendStoreError(ctx *fasthttp.RequestCtx, rel string, err error) {
	switch {
	case errors.Is(err, projectfs.ErrOutside):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			fmt.Sprintf("path %q is outside the project area: use a relative path without \"..\", .git or .heimdall", rel))
	case errors.Is(err, projectfs.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, fmt.Sprintf("%q not found", rel))
	case errors.Is(err, projectfs.ErrConflict):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
			"the file changed since you opened it: reload it, then apply your edit again")
	case errors.Is(err, projectfs.ErrTooLarge):
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge,
			fmt.Sprintf("file is larger than %d bytes: upload big files as a zip on the Upload page", projectfs.MaxFileBytes))
	case errors.Is(err, projectfs.ErrInvalid):
		msg := strings.TrimPrefix(err.Error(), projectfs.ErrInvalid.Error()+": ")
		if msg == projectfs.ErrInvalid.Error() {
			msg = fmt.Sprintf("%q is not a file that can be used here (is it a folder?)", rel)
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, msg)
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "project files: "+err.Error())
	}
}

func (h *projectFilesHandler) list(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	rawArea := string(ctx.QueryArgs().Peek("area"))
	if rawArea == "" {
		out := ProjectFileList{Entries: []projectfs.Entry{}}
		for _, a := range ProjectAreas {
			e := projectfs.Entry{Path: string(a), Dir: true}
			if st, err := os.Stat(filepath.Join(h.store.Root(), string(a))); err == nil {
				e.ModTime = st.ModTime()
			}
			out.Entries = append(out.Entries, e)
		}
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
		return
	}
	a, ok := parseArea(rawArea)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, areaError(rawArea))
		return
	}
	dir := string(ctx.QueryArgs().Peek("dir"))
	recursive := ctx.QueryArgs().GetBool("recursive")
	h.sendList(ctx, a, dir, recursive)
}

func (h *projectFilesHandler) sendList(ctx *fasthttp.RequestCtx, a projectfs.Area, dir string, recursive bool) {
	entries, err := h.store.List(ctx, a, dir, recursive)
	if err != nil {
		h.sendStoreError(ctx, dir, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ProjectFileList{Area: string(a), Dir: strings.Trim(dir, "/"), Entries: entries})
}

func (h *projectFilesHandler) get(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	a, rel, ok := h.pathParams(ctx)
	if !ok {
		return
	}
	full, err := h.store.Resolve(a, rel)
	if err != nil {
		h.sendStoreError(ctx, rel, err)
		return
	}
	if st, err := os.Stat(full); err == nil && st.IsDir() {
		h.sendList(ctx, a, rel, ctx.QueryArgs().GetBool("recursive"))
		return
	}
	data, entry, err := h.store.Read(ctx, a, rel)
	if err != nil {
		h.sendStoreError(ctx, rel, err)
		return
	}
	ctx.Response.Header.Set("ETag", strconv.Quote(entry.SHA256))
	if ctx.QueryArgs().GetBool("raw") {
		sendDownload(ctx, h.mode, path.Base(entry.Path), data)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, fileContent(a, entry, data))
}

func fileContent(a projectfs.Area, entry projectfs.Entry, data []byte) ProjectFile {
	out := ProjectFile{Area: string(a), Path: entry.Path, Entry: entry, Encoding: "utf-8", Content: string(data)}
	if !utf8.Valid(data) {
		out.Encoding, out.Content = "base64", base64.StdEncoding.EncodeToString(data)
	}
	return out
}

// sendDownload answers raw bytes as an attachment.
func sendDownload(ctx *fasthttp.RequestCtx, mode schemas.Mode, name string, data []byte) {
	ctx.Response.Header.Set(lib.ModeHeader, string(mode))
	ctx.Response.Header.Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.SetContentType("application/octet-stream")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(data)
}

// writeOpts reads the actor, the note and If-Match (header or if_match query).
func writeOpts(ctx *fasthttp.RequestCtx) projectfs.WriteOpts {
	o := projectfs.WriteOpts{Note: string(ctx.QueryArgs().Peek("note"))}
	if len(o.Note) > 200 {
		o.Note = o.Note[:200]
	}
	if p := auth.PrincipalFrom(ctx); p != nil {
		switch {
		case p.Email != "":
			o.Actor = p.Email
		case p.UserID != "":
			o.Actor = p.UserID
		case p.TokenID != "":
			o.Actor = "token:" + p.TokenID
		default:
			o.Actor = string(p.Kind)
		}
	}
	m := string(ctx.Request.Header.Peek("If-Match"))
	if m == "" {
		m = string(ctx.QueryArgs().Peek("if_match"))
	}
	m = strings.TrimPrefix(strings.TrimSpace(m), "W/")
	o.IfMatch = strings.Trim(m, `"`)
	if o.IfMatch == "*" {
		o.IfMatch = ""
	}
	return o
}

func (h *projectFilesHandler) put(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	a, rel, ok := h.pathParams(ctx)
	if !ok {
		return
	}
	body := ctx.PostBody()
	if len(body) > projectfs.MaxFileBytes {
		h.sendStoreError(ctx, rel, projectfs.ErrTooLarge)
		return
	}
	entry, err := h.store.Write(ctx, a, rel, append([]byte(nil), body...), writeOpts(ctx))
	if err != nil {
		h.sendStoreError(ctx, rel, err)
		return
	}
	ctx.Response.Header.Set("ETag", strconv.Quote(entry.SHA256))
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ProjectFileSaved{Area: string(a), Path: entry.Path, Entry: &entry})
}

func (h *projectFilesHandler) del(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	a, rel, ok := h.pathParams(ctx)
	if !ok {
		return
	}
	if err := h.store.Delete(ctx, a, rel, writeOpts(ctx)); err != nil {
		h.sendStoreError(ctx, rel, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ProjectFileSaved{Area: string(a), Path: strings.Trim(rel, "/"), Deleted: true})
}

func (h *projectFilesHandler) versions(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	a, rel, ok := h.pathParams(ctx)
	if !ok {
		return
	}
	if id := string(ctx.QueryArgs().Peek("id")); id != "" {
		data, err := h.store.ReadVersion(ctx, a, rel, id)
		if err != nil {
			h.sendStoreError(ctx, rel+"@"+id, err)
			return
		}
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, fileContent(a, projectfs.Entry{Path: strings.Trim(rel, "/"), Size: int64(len(data))}, data))
		return
	}
	vs, err := h.store.Versions(ctx, a, rel)
	if err != nil {
		h.sendStoreError(ctx, rel, err)
		return
	}
	if vs == nil {
		vs = []projectfs.Version{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ProjectFileVersions{Area: string(a), Path: strings.Trim(rel, "/"), Versions: vs})
}

// export streams every area as one zip (area/<path>); .git, .heimdall,
// symlinks and temporary files are left out.
func (h *projectFilesHandler) export(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	type file struct{ name, full string }
	var files []file
	var total int64
	for _, a := range ProjectAreas {
		entries, err := h.store.List(ctx, a, "", true)
		if err != nil {
			h.sendStoreError(ctx, string(a), err)
			return
		}
		for _, e := range entries {
			if e.Dir {
				continue
			}
			full, err := h.store.Resolve(a, e.Path)
			if err != nil {
				continue
			}
			st, err := os.Lstat(full)
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			total += st.Size()
			if total > maxExportBytes {
				lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge,
					fmt.Sprintf("project is larger than %d MiB: export it with git or bifrost-bench pull instead", maxExportBytes>>20))
				return
			}
			files = append(files, file{name: string(a) + "/" + e.Path, full: full})
		}
	}
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="project-`+time.Now().UTC().Format("20060102-150405")+`.zip"`)
	ctx.SetContentType("application/zip")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
		zw := zip.NewWriter(w)
		for _, f := range files {
			if err := addZipFile(zw, f.name, f.full); err != nil {
				break
			}
		}
		_ = zw.Close()
		_ = w.Flush()
	})
}

func addZipFile(zw *zip.Writer, name, full string) error {
	src, err := os.Open(full)
	if err != nil {
		return nil // removed since the listing: skip it
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return nil
	}
	hdr, err := zip.FileInfoHeader(st)
	if err != nil {
		return err
	}
	hdr.Name, hdr.Method = name, zip.Deflate
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, io.LimitReader(src, st.Size()))
	return err
}
