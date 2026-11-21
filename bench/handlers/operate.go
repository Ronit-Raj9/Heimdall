package handlers

// Web parity lane L20 (Operate): GET /operate/status tells the /operate page
// what this server can do (which job kinds run here, whether an evidence
// mirror is configured) so it can hide buttons that would only fail.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// OperateStatusPath is the route of the operate status.
const OperateStatusPath = APIPrefix + "/operate/status"

// OperateStatus is the body of GET /operate/status.
type OperateStatus struct {
	Hosted bool `json:"hosted"`
	// Kinds are the operate job kinds this server runs (POST /jobs).
	Kinds []string `json:"kinds"`
	// Mirror is "folder", "s3" or "" when no evidence mirror is configured.
	Mirror string `json:"mirror"`
	// MirrorLocation is the folder or s3://bucket/prefix (local mode only).
	MirrorLocation string `json:"mirror_location,omitempty"`
	GPUMode        string `json:"gpu_mode,omitempty"`
	Driver         string `json:"driver,omitempty"`
	// Hint says what to configure for what is missing, if anything.
	Hint string `json:"hint,omitempty"`
}

// operateHostedKinds are the operate kinds a hosted org runs: the doctor of
// its own suites, agents and runners, and the evidence verbs over its own
// runs folder (cmd/bifrost-bench/jobs_evidencehosted.go).
var operateHostedKinds = []string{"operate.doctor", "evidence.verify", "evidence.archive", "evidence.backup", "evidence.restore"}

// OperateBackupsPath lists the org's evidence backups; {name} downloads one.
const OperateBackupsPath = APIPrefix + "/operate/backups"

// OperateBackup is one evidence backup zip.
type OperateBackup struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// OperateBackupList is GET /operate/backups.
type OperateBackupList struct {
	Backups []OperateBackup `json:"backups"`
}

// operateBackupRe is the only file name a backup has.
var operateBackupRe = regexp.MustCompile(`^evidence-[A-Za-z0-9_-]{1,64}-[0-9]{8}-[0-9]{6}\.zip$`)

// OperateBackupDir is the folder of an org's evidence backups: exports/evidence
// under its project folder.
func OperateBackupDir(projectRoot string) string {
	return filepath.Join(projectRoot, "exports", "evidence")
}

// OperateBackupName is the file name of a backup of org taken at t.
func OperateBackupName(org string, t time.Time) string {
	return "evidence-" + org + "-" + t.UTC().Format("20060102-150405") + ".zip"
}

// ListOperateBackups lists the backups of a project folder, newest first.
func ListOperateBackups(projectRoot string) ([]OperateBackup, error) {
	ents, err := os.ReadDir(OperateBackupDir(projectRoot))
	if os.IsNotExist(err) {
		return []OperateBackup{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []OperateBackup{}
	for _, e := range ents {
		if !e.Type().IsRegular() || !operateBackupRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, OperateBackup{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime().UTC()})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].ModTime.After(out[j].ModTime)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

// operateLocalKinds are the operate kinds a local server runs; hosted runs
// only operate.doctor (the org-safe subset). Kept in step with
// cmd/bifrost-bench/jobs_operate.go.
var operateLocalKinds = []string{
	"operate.doctor", "gpu.check", "voice.check", "visual.capture", "visual.compare", "simulator.spread",
	"evidence.verify", "evidence.mirror", "evidence.archive", "evidence.backup", "evidence.restore", "k8s.status",
}

func init() {
	RegisterOrgArea("operate", func(env OrgEnv, r *router.Router) {
		r.GET(OperateStatusPath, env.R(func(ctx *fasthttp.RequestCtx) { operateStatus(ctx, env) }))
		r.GET(OperateBackupsPath, env.R(func(ctx *fasthttp.RequestCtx) { operateBackups(ctx, env) }))
		r.GET(OperateBackupsPath+"/{name}", env.R(func(ctx *fasthttp.RequestCtx) { operateBackupDownload(ctx, env) }))
		r.DELETE(OperateBackupsPath+"/{name}", env.W(func(ctx *fasthttp.RequestCtx) { operateBackupDelete(ctx, env) }))
	})
}

func buildOperateStatus(env OrgEnv) OperateStatus {
	hosted := env.Services.Hosted
	st := OperateStatus{Hosted: hosted}
	if hosted {
		st.Kinds = append([]string(nil), operateHostedKinds...)
		if env.Cfg != nil {
			switch sc := env.Cfg.Storage; {
			case sc.MirrorDir != "":
				st.Mirror = "folder" // the location stays private to the server owner
			case sc.S3.Bucket != "":
				st.Mirror = "s3"
			}
		}
		st.Hint = "Verify and back up work on your own runs; archive and restore also need the server's evidence mirror."
		return st
	}
	st.Kinds = append([]string(nil), operateLocalKinds...)
	if env.Cfg == nil {
		return st
	}
	sc := env.Cfg.Storage
	switch {
	case sc.MirrorDir != "":
		st.Mirror, st.MirrorLocation = "folder", env.Cfg.Path(sc.MirrorDir)
	case sc.S3.Bucket != "":
		st.Mirror, st.MirrorLocation = "s3", "s3://"+sc.S3.Bucket+"/"+sc.S3.Prefix
	default:
		st.Hint = "No evidence mirror: set storage.mirror_dir (a folder) or storage.s3 in the bench config to use verify, mirror, archive, backup and restore."
	}
	st.GPUMode = env.Cfg.GPU.Mode
	st.Driver = env.Cfg.Environment.Driver
	return st
}

func operateStatus(ctx *fasthttp.RequestCtx, env OrgEnv) {
	lib.SendJSON(ctx, env.Mode, fasthttp.StatusOK, buildOperateStatus(env))
}

func operateRoot(ctx *fasthttp.RequestCtx, env OrgEnv) (string, bool) {
	if env.Cfg == nil || env.Cfg.Root == "" {
		sendUnavailable(ctx, env.Mode, "evidence backups (no project folder)")
		return "", false
	}
	return env.Cfg.Root, true
}

func operateBackups(ctx *fasthttp.RequestCtx, env OrgEnv) {
	root, ok := operateRoot(ctx, env)
	if !ok {
		return
	}
	list, err := ListOperateBackups(root)
	if err != nil {
		lib.SendError(ctx, env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "backups: "+err.Error())
		return
	}
	lib.SendJSON(ctx, env.Mode, fasthttp.StatusOK, OperateBackupList{Backups: list})
}

// operateBackupPath confines {name} to the backup folder.
func operateBackupPath(ctx *fasthttp.RequestCtx, env OrgEnv) (string, string, bool) {
	root, ok := operateRoot(ctx, env)
	if !ok {
		return "", "", false
	}
	name := pathParam(ctx, "name")
	if !operateBackupRe.MatchString(name) {
		sendNotFound(ctx, env.Mode, "backup", name)
		return "", "", false
	}
	p := filepath.Join(OperateBackupDir(root), name)
	if info, err := os.Lstat(p); err != nil || !info.Mode().IsRegular() {
		sendNotFound(ctx, env.Mode, "backup", name)
		return "", "", false
	}
	return p, name, true
}

func operateBackupDownload(ctx *fasthttp.RequestCtx, env OrgEnv) {
	p, name, ok := operateBackupPath(ctx, env)
	if !ok {
		return
	}
	f, err := os.Open(p)
	if err != nil {
		lib.SendError(ctx, env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "backup: "+err.Error())
		return
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		lib.SendError(ctx, env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "backup: "+err.Error())
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/zip")
	ctx.Response.Header.Set(lib.ModeHeader, string(env.Mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	ctx.SetBodyStream(f, int(info.Size())) // fasthttp closes f when the body is sent
}

func operateBackupDelete(ctx *fasthttp.RequestCtx, env OrgEnv) {
	p, _, ok := operateBackupPath(ctx, env)
	if !ok {
		return
	}
	if err := os.Remove(p); err != nil {
		lib.SendError(ctx, env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "backup: "+err.Error())
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}
