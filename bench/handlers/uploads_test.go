package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type zfile struct {
	name string
	body string
	mode fs.FileMode
}

func makeZip(t *testing.T, files []zfile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		hdr := &zip.FileHeader{Name: f.name, Method: zip.Deflate}
		if f.mode != 0 {
			hdr.SetMode(f.mode)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadCtx(body []byte, query string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.Header.SetContentType("application/zip")
	ctx.Request.SetRequestURI("/api/bench/uploads/x" + query)
	ctx.Request.SetBody(body)
	return ctx
}

func newUploads(t *testing.T) (*uploadsHandler, string) {
	t.Helper()
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return &uploadsHandler{mode: schemas.ModeLive, storeRoot: filepath.Join(t.TempDir(), "runs"), svc: Services{Projects: ps}}, root
}

func TestUploadProjectWritesAreasAndUnwraps(t *testing.T) {
	h, root := newUploads(t)
	z := makeZip(t, []zfile{
		{name: "myproj/agents/a1/notes.md", body: "hi"},
		{name: "myproj/policies/p.txt", body: "x"},
		{name: "myproj/README.md", body: "skip"},
		{name: "__MACOSX/myproj/._x", body: "junk"},
	})
	ctx := uploadCtx(z, "")
	h.postProject(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var rep UploadFilesReport
	if err := json.Unmarshal(ctx.Response.Body(), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Written) != 2 {
		t.Fatalf("report %+v", rep)
	}
	if b, err := os.ReadFile(filepath.Join(root, "agents", "a1", "notes.md")); err != nil || string(b) != "hi" {
		t.Fatalf("agent file: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "README.md")); !os.IsNotExist(err) {
		t.Fatal("file outside the areas written")
	}
}

func TestUploadRejectsZipSlip(t *testing.T) {
	cases := map[string][]zfile{
		"dotdot":    {{name: "agents/ok.txt", body: "a"}, {name: "agents/../../evil.txt", body: "x"}},
		"absolute":  {{name: "/etc/evil", body: "x"}},
		"backslash": {{name: `agents\..\..\evil.txt`, body: "x"}},
		"heimdall":  {{name: "agents/.heimdall/versions/x", body: "x"}},
		"symlink":   {{name: "agents/link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			h, root := newUploads(t)
			for _, call := range []func(*fasthttp.RequestCtx){h.postProject, h.postRuns} {
				ctx := uploadCtx(makeZip(t, files), "")
				call(ctx)
				if ctx.Response.StatusCode() != 400 {
					t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
				}
			}
			ctx := uploadCtx(makeZip(t, files), "?name=a1")
			h.postAgent(ctx)
			if ctx.Response.StatusCode() != 400 {
				t.Fatalf("agent status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
			}
			if _, err := os.Stat(filepath.Join(root, "agents", "ok.txt")); !os.IsNotExist(err) {
				t.Fatal("part of a refused zip was written")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.txt")); !os.IsNotExist(err) {
				t.Fatal("zip-slip wrote outside the project")
			}
		})
	}
}

func TestUploadAgentNamesFromTopFolder(t *testing.T) {
	h, root := newUploads(t)
	ctx := uploadCtx(makeZip(t, []zfile{{name: "my-agent/skills/s1/SKILL.md", body: "# s"}}), "")
	h.postAgent(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "my-agent", "skills", "s1", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	// A bad name is refused.
	ctx = uploadCtx(makeZip(t, []zfile{{name: "x.md", body: "x"}}), "?name=../x")
	h.postAgent(ctx)
	if ctx.Response.StatusCode() != 400 {
		t.Fatalf("bad name status %d", ctx.Response.StatusCode())
	}
}

func TestUploadNotAZip(t *testing.T) {
	h, _ := newUploads(t)
	ctx := uploadCtx([]byte("hello"), "")
	h.postProject(ctx)
	if ctx.Response.StatusCode() != 400 || !strings.Contains(string(ctx.Response.Body()), "not a zip") {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

// recordedZip zips testdata/recorded under a top "runs/" folder, letting
// mutate change a file on the way.
func recordedZip(t *testing.T, mutate func(rel string, b []byte) []byte) []byte {
	t.Helper()
	src := filepath.Join("..", "testdata", "recorded")
	var files []zfile
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if mutate != nil {
			b = mutate(filepath.ToSlash(rel), b)
		}
		files = append(files, zfile{name: "runs/" + filepath.ToSlash(rel), body: string(b)})
		return nil
	})
	if err != nil {
		t.Skip("no recorded testdata: ", err)
	}
	return makeZip(t, files)
}

func TestUploadRunsImportsSealed(t *testing.T) {
	h, _ := newUploads(t)
	ctx := uploadCtx(recordedZip(t, nil), "")
	h.postRuns(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var rep UploadRunsReport
	if err := json.Unmarshal(ctx.Response.Body(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Imported == 0 || rep.Refused != 0 || rep.Runs == 0 {
		t.Fatalf("report %+v", rep)
	}
}

func TestUploadRunsRejectsBadSeal(t *testing.T) {
	h, _ := newUploads(t)
	tampered := false
	z := recordedZip(t, func(rel string, b []byte) []byte {
		if !tampered && strings.HasPrefix(rel, "e_smoke_sdoj/") && strings.HasSuffix(rel, "/run.json") {
			tampered = true
			return append(b, ' ')
		}
		return b
	})
	if !tampered {
		t.Skip("no run.json to tamper with")
	}
	ctx := uploadCtx(z, "")
	h.postRuns(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var rep UploadRunsReport
	if err := json.Unmarshal(ctx.Response.Body(), &rep); err != nil {
		t.Fatal(err)
	}
	refused := false
	for _, e := range rep.Experiments {
		if e.ExpID == "e_smoke_sdoj" {
			refused = e.Status == "refused" && e.Reason != ""
		}
	}
	if !refused {
		t.Fatalf("tampered experiment not refused: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(h.storeRoot, "e_smoke_sdoj")); !os.IsNotExist(err) {
		t.Fatal("tampered experiment kept in the store")
	}
}

// Gap G13: a hosted import must refresh the org's indexes (the CLI org
// import's db.SyncOrg) so the experiments show at once.
func TestUploadRunsReindexes(t *testing.T) {
	h, _ := newUploads(t)
	calls := 0
	h.svc.Reindex = func(context.Context) error { calls++; return nil }
	ctx := uploadCtx(recordedZip(t, nil), "")
	h.postRuns(ctx)
	if ctx.Response.StatusCode() != 200 || calls != 1 {
		t.Fatalf("status %d, reindex calls %d: %s", ctx.Response.StatusCode(), calls, ctx.Response.Body())
	}
	// A second upload of the same experiments imports nothing: no reindex.
	ctx = uploadCtx(recordedZip(t, nil), "")
	h.postRuns(ctx)
	if calls != 1 {
		t.Fatalf("reindex on a no-op import: %d calls", calls)
	}
}

func TestUploadRunsReindexFailureWarns(t *testing.T) {
	h, _ := newUploads(t)
	h.svc.Reindex = func(context.Context) error { return errors.New("db down") }
	ctx := uploadCtx(recordedZip(t, nil), "")
	h.postRuns(ctx)
	var rep UploadRunsReport
	if err := json.Unmarshal(ctx.Response.Body(), &rep); err != nil {
		t.Fatal(err)
	}
	if ctx.Response.StatusCode() != 200 || rep.Imported == 0 || !strings.Contains(rep.IndexWarning, "db down") {
		t.Fatalf("status %d report %+v", ctx.Response.StatusCode(), rep)
	}
}
