package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if !st.Hosted || st.MirrorLocation != "" || st.Mirror != "folder" || strings.Contains(st.Hint, "/secret") {
		t.Fatalf("hosted leaks server details: %+v", st)
	}
	if strings.Join(st.Kinds, ",") != "operate.doctor,evidence.verify,evidence.archive,evidence.backup,evidence.restore" {
		t.Fatalf("hosted kinds %v", st.Kinds)
	}
}

func TestOperateBackupsListDownloadConfined(t *testing.T) {
	root := t.TempDir()
	dir := OperateBackupDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := OperateBackupName("org_a", time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC))
	if err := os.WriteFile(filepath.Join(dir, name), []byte("PK zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}}
	var ctx fasthttp.RequestCtx
	operateBackups(&ctx, env)
	var list OperateBackupList
	if err := json.Unmarshal(ctx.Response.Body(), &list); err != nil || len(list.Backups) != 1 || list.Backups[0].Name != name {
		t.Fatalf("list %s %v", ctx.Response.Body(), err)
	}
	get := func(n string) *fasthttp.RequestCtx {
		var c fasthttp.RequestCtx
		c.SetUserValue("name", n)
		operateBackupDownload(&c, env)
		return &c
	}
	if c := get(name); c.Response.StatusCode() != fasthttp.StatusOK || string(c.Response.Body()) != "PK zip" ||
		!strings.Contains(string(c.Response.Header.Peek("Content-Disposition")), name) {
		t.Fatalf("download %d %q", c.Response.StatusCode(), c.Response.Body())
	}
	for _, bad := range []string{"notes.txt", "../" + name, "evidence-org_b-20261121-100000.zip"} {
		if c := get(bad); c.Response.StatusCode() != fasthttp.StatusNotFound {
			t.Errorf("%s: status %d, want 404", bad, c.Response.StatusCode())
		}
	}
}
