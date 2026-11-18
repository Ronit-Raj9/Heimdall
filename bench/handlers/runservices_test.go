package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// servicesIndex is a sealed test index whose run r_t01_A had a compose stack:
// db and cache with captured logs, and a task service without one.
func servicesIndex(bad map[string]error) sealIndex {
	idx := testIndex()
	idx.files["r_t01_A"][FileEvidence] = []byte(`{"services":[` +
		`{"id":"db","image":"postgres:18","image_id":"sha256:aa","ready":true},` +
		`{"id":"cache","image":"redis:7-alpine","ready":true},` +
		`{"id":"desk","role":"mcp","image":"desk:1","ready":false,"error":"not ready after 60s","proxied":true}]}`)
	idx.files["r_t01_A"]["services/db.log"] = []byte("2026-11-18T08:00:00Z database system is ready to accept connections\n<script>alert(1)</script>\n")
	idx.files["r_t01_A"]["services/cache.log"] = []byte("Ready to accept connections tcp\n")
	return sealIndex{fakeIndex: idx, bad: bad}
}

func TestRunServices(t *testing.T) {
	d := fullDeps()
	d.index = servicesIndex(nil)
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/services", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatal(err)
	}
	checkKeys(t, "RunServices", body, loadKeys(t, "RunServices"), true)
	for i, obj := range objectsAt(t, body, "services[]") {
		checkKeys(t, fmt.Sprintf("RunServices.services #%d", i), obj, loadKeys(t, "RunService"), false)
	}
	var out RunServices
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if out.RunID != "r_t01_A" || !out.Sealed || len(out.Services) != 3 {
		t.Fatalf("out %+v", out)
	}
	if db := out.Services[0]; db.LogBytes == nil || *db.LogBytes == 0 || db.ImageID != "sha256:aa" {
		t.Fatalf("db %+v", db)
	}
	if desk := out.Services[2]; desk.LogBytes != nil || desk.Ready || desk.Error == "" {
		t.Fatalf("desk %+v", desk)
	}
	// A run without services answers an empty list, not null.
	resp = c.do(t, "GET", APIPrefix+"/runs/r_t02_A/services", nil, nil)
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"services":[]`) {
		t.Fatalf("no services: %d %s", resp.StatusCode(), resp.Body())
	}
}

func TestRunServiceLog(t *testing.T) {
	d := fullDeps()
	d.index = servicesIndex(map[string]error{"r_t01_B": fmt.Errorf("services/db.log: %w", schemas.ErrSealMismatch)})
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/services/db/log", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	if ct := string(resp.Header.Peek("Content-Type")); !strings.HasPrefix(ct, "text/plain") || string(resp.Header.Peek("X-Content-Type-Options")) != "nosniff" {
		t.Fatalf("headers: %q nosniff %q", ct, resp.Header.Peek("X-Content-Type-Options"))
	}
	if !strings.Contains(string(resp.Body()), "ready to accept connections") {
		t.Fatalf("body %q", resp.Body())
	}
	for path, want := range map[string]int{
		"/runs/r_t01_A/services/desk/log":      404, // a service without a captured log
		"/runs/r_t01_A/services/..%2Fseal/log": 404, // not a service id
		"/runs/r_t01_A/services/DB/log":        404,
		"/runs/r_t01_B/services/db/log":        409, // the run does not match its seal
		"/runs/r_nope/services/db/log":         404,
	} {
		if r := c.do(t, "GET", APIPrefix+path, nil, nil); r.StatusCode() != want {
			t.Errorf("%s: status %d, want %d: %s", path, r.StatusCode(), want, r.Body())
		}
	}
	// An unsealed run is refused.
	idx := servicesIndex(nil)
	for i := range idx.runs {
		idx.runs[i].Sealed = false
	}
	d.index = idx
	c = newTestClient(t, d)
	if r := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/services/db/log", nil, nil); r.StatusCode() != 409 {
		t.Fatalf("unsealed: %d", r.StatusCode())
	}
}
