package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// artifactIndex is a fakeIndex that also reads artifact files and verifies
// seals, as the store's index does.
type artifactIndex struct {
	*fakeIndex
	verifyErr error
	mu        sync.Mutex
	read      []string // every artifact path RunArtifact was asked for
}

func (a *artifactIndex) RunArtifact(runID, id, name string) ([]byte, error) {
	p := utils.ArtifactRunPath(id, name)
	a.mu.Lock()
	a.read = append(a.read, p)
	a.mu.Unlock()
	if data, ok := a.files[runID][p]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("run %s artifact %s: %w", runID, p, schemas.ErrNotFound)
}

func (a *artifactIndex) VerifySeal(context.Context, string) error { return a.verifyErr }

func (a *artifactIndex) reads() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.read...)
}

// artifactBytes is binary content with a NUL and bytes that are not UTF-8.
var artifactBytes = []byte("PK\x03\x04\x00\x00<script>alert(1)</script>\xff\xfe\x00end")

// artifactFixture is a sealed run r_t01_A with artifact files in every
// state, and r_t02_A as an unsealed run that lists a good file.
func artifactFixture(t *testing.T) *artifactIndex {
	t.Helper()
	idx := testIndex()
	for i := range idx.runs {
		if idx.runs[i].Run.RunID == "r_t02_A" {
			idx.runs[i].Sealed, idx.runs[i].SealHash = false, ""
		}
	}
	good := utils.SHA256Bytes(artifactBytes)
	other := utils.SHA256Bytes([]byte("other"))
	files := []schemas.ArtifactFile{
		{ID: "report", Name: "report.bin", Path: "artifacts/report/report.bin", Source: "out/report.bin", SHA256: good, Bytes: int64(len(artifactBytes))},
		{ID: "chart", Source: "out/*.svg", Error: "missing"},
		{ID: "mesh", Name: "big.obj", Source: "out/big.obj", Bytes: 99, Error: "over the size cap"},
		{ID: "moved", Name: "m.txt", Path: "artifacts/report/report.bin", Source: "m.txt", SHA256: good, Bytes: 1},
		{ID: "tampered", Name: "t.bin", Path: "artifacts/tampered/t.bin", Source: "t.bin", SHA256: other, Bytes: 5},
		{ID: "resealed", Name: "r.bin", Path: "artifacts/resealed/r.bin", Source: "r.bin", SHA256: good, Bytes: 5},
		{ID: "unlisted", Name: "u.bin", Path: "artifacts/unlisted/u.bin", Source: "u.bin", SHA256: good, Bytes: 5},
		{ID: "prefixed", Name: "p.bin", Path: "artifacts/prefixed/p.bin", Source: "p.bin", SHA256: "sha256:" + good, Bytes: 5},
	}
	ev, err := json.Marshal(map[string]any{"requests": map[string]any{"complete": true}, "artifacts": files})
	if err != nil {
		t.Fatal(err)
	}
	seal, err := json.Marshal(schemas.SealRecord{RunID: "r_t01_A", Files: map[string]string{
		"artifacts/report/report.bin": good,
		"artifacts/tampered/t.bin":    good,
		"artifacts/resealed/r.bin":    other,
		"artifacts/prefixed/p.bin":    good,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{"r_t01_A", "r_t02_A"} {
		idx.files[runID][FileEvidence] = ev
		idx.files[runID][FileSeal] = seal
		for _, p := range []string{"artifacts/report/report.bin", "artifacts/tampered/t.bin", "artifacts/resealed/r.bin", "artifacts/unlisted/u.bin", "artifacts/prefixed/p.bin"} {
			idx.files[runID][p] = artifactBytes
		}
	}
	return &artifactIndex{fakeIndex: idx}
}

func artifactURL(runID, id, name string) string {
	return APIPrefix + "/runs/" + runID + "/artifacts/" + id + "/" + name
}

func TestRunArtifactServesSealedFile(t *testing.T) {
	idx := artifactFixture(t)
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	for _, tc := range []struct{ id, name string }{{"report", "report.bin"}, {"prefixed", "p.bin"}} {
		resp := c.do(t, "GET", artifactURL("r_t01_A", tc.id, tc.name), nil, nil)
		if resp.StatusCode() != 200 {
			t.Fatalf("%s: status = %d: %s", tc.id, resp.StatusCode(), resp.Body())
		}
		if !bytes.Equal(resp.Body(), artifactBytes) {
			t.Errorf("%s: body = %q, want %q", tc.id, resp.Body(), artifactBytes)
		}
		want := map[string]string{
			"Content-Type":            "application/octet-stream",
			"X-Content-Type-Options":  "nosniff",
			"Content-Disposition":     `attachment; filename="` + tc.name + `"`,
			"Content-Security-Policy": "sandbox",
			"Cache-Control":           "no-store",
			"X-Bench-Mode":            string(testMode),
		}
		for k, v := range want {
			if got := string(resp.Header.Peek(k)); got != v {
				t.Errorf("%s: header %s = %q, want %q", tc.id, k, got, v)
			}
		}
	}
}

func TestRunArtifactRefuses(t *testing.T) {
	tests := []struct {
		name, path string
		status     int
		verifyErr  error
		msg        string // part of the error message
	}{
		{name: "unknown run", path: artifactURL("r_none", "report", "report.bin"), status: 404},
		{name: "unknown artifact id", path: artifactURL("r_t01_A", "nope", "report.bin"), status: 404},
		{name: "unknown name", path: artifactURL("r_t01_A", "report", "other.bin"), status: 404},
		{name: "error entry with name", path: artifactURL("r_t01_A", "mesh", "big.obj"), status: 404, msg: "over the size cap"},
		{name: "error entry without name", path: artifactURL("r_t01_A", "chart", "x.svg"), status: 404},
		{name: "evidence path elsewhere", path: artifactURL("r_t01_A", "moved", "m.txt"), status: 404},
		{name: "unsealed run", path: artifactURL("r_t02_A", "report", "report.bin"), status: 409, msg: "not sealed"},
		{name: "evidence hash differs", path: artifactURL("r_t01_A", "tampered", "t.bin"), status: 409, msg: "does not match"},
		{name: "seal hash differs", path: artifactURL("r_t01_A", "resealed", "r.bin"), status: 409, msg: "does not match"},
		{name: "not in seal", path: artifactURL("r_t01_A", "unlisted", "u.bin"), status: 409, msg: "not in seal.json"},
		{name: "folder does not match seal", path: artifactURL("r_t01_A", "report", "report.bin"), status: 409,
			verifyErr: fmt.Errorf("file workspace.diff changed: %w", schemas.ErrSealMismatch), msg: "workspace.diff changed"},
		{name: "seal check broke", path: artifactURL("r_t01_A", "report", "report.bin"), status: 500,
			verifyErr: errors.New("disk on fire"), msg: "disk on fire"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := artifactFixture(t)
			idx.verifyErr = tt.verifyErr
			d := fullDeps()
			d.index = idx
			c := newTestClient(t, d)
			resp := c.do(t, "GET", tt.path, nil, nil)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
			if bytes.Contains(resp.Body(), artifactBytes) || string(resp.Header.ContentType()) == "application/octet-stream" {
				t.Fatalf("refused request served the file: %q", resp.Body())
			}
			if tt.msg != "" && !strings.Contains(string(resp.Body()), tt.msg) {
				t.Errorf("body = %s, want it to mention %q", resp.Body(), tt.msg)
			}
		})
	}
}

func TestRunArtifactTraversal(t *testing.T) {
	idx := artifactFixture(t)
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	base := APIPrefix + "/runs/r_t01_A/artifacts/"
	paths := []string{
		base + "report/..%2F..%2Fseal.json",
		base + "report/%2e%2e",
		base + "report/..",
		base + "%2e%2e/report.bin",
		base + "..%2Freport/report.bin",
		base + "report/..%5C..%5Cseal.json",
		base + "report/.hidden",
		base + "report/report.bin%00",
		base + "report/report.bin%2Fx",
		base + "report%2Freport.bin/x",
		base + "Report/report.bin",
		base + "report/" + strings.Repeat("a", 200),
		base + "report/a%20b",
		base + "report/../../files/x",
		base + "report/report.bin/extra",
	}
	for _, p := range paths {
		resp := c.do(t, "GET", p, nil, nil)
		if resp.StatusCode() != 404 {
			t.Errorf("%s: status = %d, want 404: %s", p, resp.StatusCode(), resp.Body())
		}
		if bytes.Contains(resp.Body(), artifactBytes) {
			t.Errorf("%s: served artifact bytes", p)
		}
	}
	if got := idx.reads(); len(got) != 0 {
		t.Errorf("RunArtifact was called for refused paths: %v", got)
	}
}

func TestRunArtifactNeedsReader(t *testing.T) {
	idx := artifactFixture(t)
	d := fullDeps()
	d.index = idx.fakeIndex // no RunArtifact
	c := newTestClient(t, d)
	resp := c.do(t, "GET", artifactURL("r_t01_A", "report", "report.bin"), nil, nil)
	if resp.StatusCode() != 503 {
		t.Fatalf("status = %d, want 503: %s", resp.StatusCode(), resp.Body())
	}
}

func TestRunArtifactMissingFile(t *testing.T) {
	idx := artifactFixture(t)
	delete(idx.files["r_t01_A"], "artifacts/report/report.bin")
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	resp := c.do(t, "GET", artifactURL("r_t01_A", "report", "report.bin"), nil, nil)
	if resp.StatusCode() != 404 {
		t.Fatalf("status = %d, want 404: %s", resp.StatusCode(), resp.Body())
	}
}

func TestRunArtifactNoEvidence(t *testing.T) {
	idx := artifactFixture(t)
	delete(idx.files["r_t01_A"], FileEvidence)
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	resp := c.do(t, "GET", artifactURL("r_t01_A", "report", "report.bin"), nil, nil)
	if resp.StatusCode() != 404 {
		t.Fatalf("status = %d, want 404: %s", resp.StatusCode(), resp.Body())
	}
	// fakeIndex reports a missing file as fs.ErrNotExist, as the store's index may.
	if _, err := idx.RunFile("r_t01_A", FileEvidence); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("fixture: %v", err)
	}
}

// TestRunDetailArtifactsContract checks the run detail's artifact list
// against the contract key lists (RunDetail.keys, ArtifactFile.keys).
func TestRunDetailArtifactsContract(t *testing.T) {
	idx := artifactFixture(t)
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	body := decodeBody(t, resp)
	checkKeys(t, "RunDetail", body, loadKeys(t, "RunDetail"), true)
	files := objectsAt(t, body, "artifacts[]")
	if len(files) != 8 {
		t.Fatalf("artifacts = %v", body["artifacts"])
	}
	for i, obj := range files {
		checkKeys(t, fmt.Sprintf("RunDetail.artifacts #%d", i), obj, loadKeys(t, "ArtifactFile"), false)
	}
}
