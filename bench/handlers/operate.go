package handlers

// Web parity lane L20 (Operate): GET /operate/status tells the /operate page
// what this server can do (which job kinds run here, whether an evidence
// mirror is configured) so it can hide buttons that would only fail.

import (
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
	})
}

func buildOperateStatus(env OrgEnv) OperateStatus {
	hosted := env.Services.Hosted
	st := OperateStatus{Hosted: hosted}
	if hosted {
		st.Kinds = []string{"operate.doctor"}
		st.Hint = "Hosted organizations get the doctor checks of their own suites and agents; server operations (evidence, GPU, Kubernetes) are run by the server owner."
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
