package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestInvestigationsRoutes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "investigations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	for i, exp := range []string{"e1", "e2", "e1"} {
		rep := investigator.Report{ID: "job_" + string(rune('a'+i)), ExpID: exp, Runs: []string{"r1", "r2"}, Items: []investigator.Item{{ID: "i0"}}, SpentUSD: 0.5, CreatedAt: at.Add(time.Duration(i) * time.Hour), Note: investigator.ReportNote}
		b, _ := json.Marshal(rep)
		if err := os.WriteFile(filepath.Join(dir, rep.ID+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "secret.json"), []byte(`{"id":"x"}`), 0o644)
	h := &investigationsHandler{mode: schemas.Mode("live"), root: root}
	r := router.New()
	r.GET(APIPrefix+"/investigations", h.list)
	r.GET(APIPrefix+"/investigations/{id}", h.get)

	code, body := abCall(r, "GET", APIPrefix+"/investigations?exp=e1", nil)
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	var list InvestigationList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Investigations) != 2 || list.Investigations[0].ID != "job_c" || list.Investigations[1].ID != "job_a" || list.Investigations[0].Runs != 2 || list.Investigations[0].Items != 1 {
		t.Fatalf("list %+v", list)
	}
	code, body = abCall(r, "GET", APIPrefix+"/investigations/job_b", nil)
	if code != 200 {
		t.Fatalf("get: %d %s", code, body)
	}
	var rep investigator.Report
	if err := json.Unmarshal(body, &rep); err != nil || rep.ExpID != "e2" || rep.Note != investigator.ReportNote {
		t.Fatalf("get: %v %+v", err, rep)
	}
	for _, bad := range []string{"nope", "..%2Fsecret", ".hidden"} {
		if code, _ := abCall(r, "GET", APIPrefix+"/investigations/"+bad, nil); code != 404 {
			t.Fatalf("%s: %d, want 404", bad, code)
		}
	}
	empty := &investigationsHandler{mode: schemas.Mode("live"), root: t.TempDir()}
	r2 := router.New()
	r2.GET(APIPrefix+"/investigations", empty.list)
	code, body = abCall(r2, "GET", APIPrefix+"/investigations", nil)
	if code != 200 || !json.Valid(body) {
		t.Fatalf("empty list: %d %s", code, body)
	}
	var el InvestigationList
	json.Unmarshal(body, &el)
	if el.Investigations == nil || len(el.Investigations) != 0 {
		t.Fatalf("empty list %s", body)
	}
}
