package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
)

// ProjectManifestPath lists every project file with its sha256, so the CLI
// (`bifrost-bench push` / `pull`) can sync only what changed.
const ProjectManifestPath = APIPrefix + "/project/manifest"

// MaxManifestFiles caps one manifest; a project past it is refused with 413
// so a sync never works from a truncated list.
const MaxManifestFiles = 20000

// ProjectAreas are the synced project areas, in manifest order.
var ProjectAreas = []projectfs.Area{
	projectfs.AreaAgents, projectfs.AreaBenchmarks, projectfs.AreaExperiments,
	projectfs.AreaPolicies, projectfs.AreaUploads,
}

// ManifestFile is one file of the project manifest.
type ManifestFile struct {
	Area   string `json:"area"`
	Path   string `json:"path"` // area-relative, slash separated
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ProjectManifest is the GET /project/manifest body.
type ProjectManifest struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Files       []ManifestFile `json:"files"`
}

func init() {
	RegisterOrgArea("projectsync", func(env OrgEnv, r *router.Router) {
		r.GET(ProjectManifestPath, env.R(func(ctx *fasthttp.RequestCtx) { serveProjectManifest(env, ctx) }))
	})
	APIRoutes = append(APIRoutes, RouteDoc{
		Method: "GET", Path: ProjectManifestPath, Tag: "project",
		Summary:  "Every project file (agents, benchmarks, experiments, policies, uploads) with its sha256, for CLI push/pull",
		Query:    []QueryDoc{{Name: "area", Description: "only this area (agents, benchmarks, experiments, policies, uploads)"}},
		Response: ProjectManifest{},
	})
}

func serveProjectManifest(env OrgEnv, ctx *fasthttp.RequestCtx) {
	st := env.Services.Projects
	if st == nil {
		lib.SendError(ctx, env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"project file store is not configured on this server: check that the org project folder exists")
		return
	}
	areas := ProjectAreas
	if a := string(ctx.QueryArgs().Peek("area")); a != "" {
		areas = nil
		for _, k := range ProjectAreas {
			if string(k) == a {
				areas = []projectfs.Area{k}
			}
		}
		if areas == nil {
			lib.SendError(ctx, env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
				"unknown area "+a+": use agents, benchmarks, experiments, policies or uploads")
			return
		}
	}
	// A plain context: fasthttp.RequestCtx's Done/Err need a live server.
	bctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	m, err := BuildProjectManifest(bctx, st, areas)
	if errors.Is(err, projectfs.ErrTooLarge) {
		lib.SendError(ctx, env.Mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge,
			"project has more files than one manifest lists; use the export zip instead")
		return
	}
	if err != nil {
		lib.SendError(ctx, env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "build project manifest: "+err.Error())
		return
	}
	lib.SendJSON(ctx, env.Mode, fasthttp.StatusOK, m)
}

// BuildProjectManifest hashes every regular file of the given areas. Each
// file is opened through Store.Resolve, so the manifest never reaches outside
// the project (symlinks that escape are skipped).
func BuildProjectManifest(ctx context.Context, st projectfs.Store, areas []projectfs.Area) (ProjectManifest, error) {
	out := ProjectManifest{GeneratedAt: time.Now().UTC(), Files: []ManifestFile{}}
	for _, a := range areas {
		// A missing area folder is an empty area (checked first: the store's
		// Resolve may refuse a base folder that does not exist yet).
		if fi, err := os.Stat(filepath.Join(st.Root(), string(a))); err != nil || !fi.IsDir() {
			continue
		}
		ents, err := st.List(ctx, a, "", true)
		if err != nil {
			if errors.Is(err, projectfs.ErrNotFound) {
				continue
			}
			return out, err
		}
		for _, e := range ents {
			if e.Dir {
				continue
			}
			if err := ctx.Err(); err != nil {
				return out, err
			}
			full, err := st.Resolve(a, e.Path)
			if err != nil {
				continue // escaping symlink or reserved name: never synced
			}
			sum, size, err := hashRegularFile(full)
			if err != nil {
				continue // vanished, unreadable or not a regular file
			}
			if len(out.Files) >= MaxManifestFiles {
				return out, projectfs.ErrTooLarge
			}
			out.Files = append(out.Files, ManifestFile{Area: string(a), Path: e.Path, Size: size, SHA256: sum})
		}
	}
	return out, nil
}

func hashRegularFile(full string) (string, int64, error) {
	f, err := os.Open(full)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("not a regular file")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
