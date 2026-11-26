package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/plaintext"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestReceiptAndStepsServedPlain checks that run receipts and run steps
// served by a hosted server carry no gateway slot name built from the
// organization id and no server path, while the files keep them (wave 4,
// F1.3, p06).
func TestReceiptAndStepsServedPlain(t *testing.T) {
	saved := plaintext.DefaultRoots
	t.Cleanup(func() { plaintext.DefaultRoots = saved })
	plaintext.DefaultRoots = func() []string { return []string{"/var/lib/heimdall/runs"} }
	idx := stepsFixture(t)
	slot := "slot org_org01m7yyh2wft8r2x5tjyxthksbj_openai_20: 503 from conversation call 2"
	dir := filepath.Join(idx.root, "r_t01_A")
	receipts := `[{"probe_id":"p1","layer":"gateway","steps":{"fired":{"state":"yes","detail":"` + slot + `"}}}]`
	if err := os.WriteFile(filepath.Join(dir, FileReceipts), []byte(receipts), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := `{"seq":1,"ts":"2026-11-15T10:00:01Z","kind":"model_turn","source":"gateway","proof":"verified","turn":1,"summary":"` + slot + `","counted":true}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, schemas.StepsFile), []byte(steps), 0o644); err != nil {
		t.Fatal(err)
	}
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	for _, uri := range []string{APIPrefix + "/runs/r_t01_A/receipts", APIPrefix + "/runs/r_t01_A/steps"} {
		resp := c.do(t, "GET", uri, nil, nil)
		body := string(resp.Body())
		if resp.StatusCode() != 200 || !strings.Contains(body, "provider slot openai 20: 503") || strings.Contains(body, "org_org") {
			t.Errorf("%s: %d %s", uri, resp.StatusCode(), body)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, FileReceipts)); !strings.Contains(string(raw), "org_org") {
		t.Fatal("the receipts file changed")
	}
}
