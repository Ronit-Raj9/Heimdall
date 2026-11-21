package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCompliancePacksListAndDownload(t *testing.T) {
	root := t.TempDir()
	h := &compliancePacksHandler{mode: "live", root: root}
	ctx := integCtx("GET", "", nil)
	h.list(ctx)
	if ctx.Response.StatusCode() != 200 || string(ctx.Response.Body()) == "" {
		t.Fatalf("empty list %d", ctx.Response.StatusCode())
	}
	dir := CompliancePacksDir(root)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "compliance-rel_1.md"), []byte("# pack\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "compliance-rel_1.json"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "compliance-secret.md"), []byte("no"), 0o644)
	_ = os.Symlink(filepath.Join(root, "compliance-secret.md"), filepath.Join(dir, "compliance-link.md"))

	ctx = integCtx("GET", "", nil)
	h.list(ctx)
	var got CompliancePackFiles
	if err := json.Unmarshal(ctx.Response.Body(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Packs) != 2 {
		t.Fatalf("packs %+v", got.Packs)
	}
	for _, p := range got.Packs {
		if p.ReleaseID != "rel_1" || (p.Format != "md" && p.Format != "json") {
			t.Fatalf("pack %+v", p)
		}
	}

	ctx = integCtx("GET", "", map[string]string{"name": "compliance-rel_1.md"})
	h.download(ctx)
	if ctx.Response.StatusCode() != 200 || string(ctx.Response.Body()) != "# pack\n" {
		t.Fatalf("download %d", ctx.Response.StatusCode())
	}
	for _, bad := range []string{"../compliance-secret.md", "compliance-link.md", "notes.txt", "compliance-.md", ".."} {
		ctx = integCtx("GET", "", map[string]string{"name": bad})
		h.download(ctx)
		if ctx.Response.StatusCode() != 404 {
			t.Errorf("%s served: %d", bad, ctx.Response.StatusCode())
		}
	}
}
