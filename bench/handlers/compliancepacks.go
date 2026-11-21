package handlers

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Compliance packs (gap G08): the release.compliance job writes
// exports/compliance/compliance-<release_id>.md and .json under the project
// folder; the Releases page lists them here and downloads them. exports/ is
// not a project files area, so these two read-only routes serve that one
// folder.

// CompliancePacksDir is where release.compliance writes its packs.
func CompliancePacksDir(projectRoot string) string {
	return filepath.Join(projectRoot, "exports", "compliance")
}

// maxCompliancePackBytes caps a download (a pack is a few hundred KiB).
const maxCompliancePackBytes = 32 << 20

// CompliancePackFile is one pack file.
type CompliancePackFile struct {
	Name      string    `json:"name"`
	ReleaseID string    `json:"release_id"`
	Format    string    `json:"format"` // "md" or "json"
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// CompliancePackFiles is GET /compliance-packs.
type CompliancePackFiles struct {
	Packs []CompliancePackFile `json:"packs"`
}

// compliancePackOf parses compliance-<release_id>.(md|json); ok false for any other name.
func compliancePackOf(name string) (releaseID, format string, ok bool) {
	if !validID(name) || !strings.HasPrefix(name, "compliance-") {
		return "", "", false
	}
	base := strings.TrimPrefix(name, "compliance-")
	for _, ext := range []string{"md", "json"} {
		if id, found := strings.CutSuffix(base, "."+ext); found && id != "" && !strings.HasPrefix(id, ".") {
			return id, ext, true
		}
	}
	return "", "", false
}

// ListCompliancePacks lists a project's compliance packs, newest first.
func ListCompliancePacks(projectRoot string) ([]CompliancePackFile, error) {
	ents, err := os.ReadDir(CompliancePacksDir(projectRoot))
	if errors.Is(err, os.ErrNotExist) {
		return []CompliancePackFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []CompliancePackFile{}
	for _, e := range ents {
		id, format, ok := compliancePackOf(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, CompliancePackFile{Name: e.Name(), ReleaseID: id, Format: format, Size: info.Size(), CreatedAt: info.ModTime().UTC()})
	}
	slices.SortFunc(out, func(a, b CompliancePackFile) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

type compliancePacksHandler struct {
	mode schemas.Mode
	root string
}

func init() {
	RegisterOrgArea("compliancepacks", func(env OrgEnv, r *router.Router) {
		h := &compliancePacksHandler{mode: env.Mode}
		if env.Cfg != nil {
			h.root = env.Cfg.Root
		}
		r.GET(APIPrefix+"/compliance-packs", env.R(h.list))
		r.GET(APIPrefix+"/compliance-packs/{name}", env.R(h.download))
	})
}

func (h *compliancePacksHandler) list(ctx *fasthttp.RequestCtx) {
	if h.root == "" {
		sendUnavailable(ctx, h.mode, "compliance packs (no project folder)")
		return
	}
	xs, err := ListCompliancePacks(h.root)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "compliance packs: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, CompliancePackFiles{Packs: xs})
}

func (h *compliancePacksHandler) download(ctx *fasthttp.RequestCtx) {
	if h.root == "" {
		sendUnavailable(ctx, h.mode, "compliance packs (no project folder)")
		return
	}
	name := pathParam(ctx, "name")
	_, format, ok := compliancePackOf(name)
	if !ok {
		sendNotFound(ctx, h.mode, "compliance pack", name)
		return
	}
	p := filepath.Join(CompliancePacksDir(h.root), name)
	info, err := os.Lstat(p) // a symlink is never followed out of the folder
	if err != nil || !info.Mode().IsRegular() {
		sendNotFound(ctx, h.mode, "compliance pack", name)
		return
	}
	if info.Size() > maxCompliancePackBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeBadRequest, "compliance pack is larger than 32 MiB; open it in the project folder")
		return
	}
	body, err := os.ReadFile(p)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "compliance pack: "+err.Error())
		return
	}
	ctype := "text/markdown; charset=utf-8"
	if format == "json" {
		ctype = "application/json"
	}
	(&ExportHandler{mode: h.mode}).download(ctx, ctype, name, body)
}
