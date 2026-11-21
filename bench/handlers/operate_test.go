package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

func TestOperateStatusLocalNoMirror(t *testing.T) {
	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}}
	var ctx fasthttp.RequestCtx
	operateStatus(&ctx, env)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status %d", ctx.Response.StatusCode())
	}
	var st OperateStatus
	if err := json.Unmarshal(ctx.Response.Body(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Hosted || st.Mirror != "" || !strings.Contains(st.Hint, "storage.mirror_dir") {
		t.Fatalf("unexpected %+v", st)
	}
	if len(st.Kinds) != len(operateLocalKinds) {
		t.Fatalf("kinds %v", st.Kinds)
	}
}

func TestOperateStatusFolderMirror(t *testing.T) {
	cfg := &lib.Config{Root: "/srv/bench"}
	cfg.Storage.MirrorDir = "mirror"
	st := buildOperateStatus(OrgEnv{Cfg: cfg})
	if st.Mirror != "folder" || st.MirrorLocation != cfg.Path("mirror") || st.Hint != "" {
		t.Fatalf("unexpected %+v", st)
	}
}

func TestOperateStatusHostedHidesServerDetails(t *testing.T) {
	cfg := &lib.Config{Root: "/srv/bench"}
	cfg.Storage.MirrorDir = "/secret/mirror"
	st := buildOperateStatus(OrgEnv{Cfg: cfg, Services: Services{Hosted: true}})
	if !st.Hosted || st.MirrorLocation != "" || st.Mirror != "" {
		t.Fatalf("hosted leaks server details: %+v", st)
	}
	if len(st.Kinds) != 1 || st.Kinds[0] != "operate.doctor" {
		t.Fatalf("hosted kinds %v", st.Kinds)
	}
}
