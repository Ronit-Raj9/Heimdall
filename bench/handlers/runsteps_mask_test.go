package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestMaskKeyShapedStepText(t *testing.T) {
	// Invented key-shaped strings, not real keys.
	cases := []string{
		`curl -H "Authorization: Bearer sk-bf-abcd1234efgh" http://gw/v1`,
		`export KEY=sk-bf-zz99`,
		`OPENAI_API_KEY=sk-testFAKEFAKEFAKE0000 python run.py`,
	}
	for _, c := range cases {
		got := maskKeyShaped(c)
		for _, secret := range []string{"abcd1234efgh", "zz99", "testFAKEFAKEFAKE0000"} {
			if strings.Contains(got, secret) {
				t.Fatalf("mask(%q) = %q still holds %q", c, got, secret)
			}
		}
		if !strings.Contains(got, "[masked]") {
			t.Fatalf("mask(%q) = %q has no mask", c, got)
		}
	}
	if got := maskKeyShaped("ls -la /workspace"); got != "ls -la /workspace" {
		t.Fatalf("plain text changed: %q", got)
	}
	steps := []schemas.StepRecord{{Summary: "bash: echo sk-bf-abcdef0123"}}
	maskStepSecrets(steps)
	if strings.Contains(steps[0].Summary, "abcdef0123") {
		t.Fatalf("step summary not masked: %q", steps[0].Summary)
	}
}

func TestRunStepsRouteMasksVirtualKey(t *testing.T) {
	ix := stepsFixture(t)
	dir := filepath.Join(ix.root, "r_t01_A")
	row := `{"seq":9,"ts":"2026-11-15T10:00:09Z","kind":"command","source":"process_observer","proof":"strong","turn":1,"summary":"curl -H 'Authorization: Bearer sk-bf-inventedKEY42abc' http://gw"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, schemas.StepsFile), []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	d := fullDeps()
	d.index = ix
	c := newTestClient(t, d)
	for _, path := range []string{"/runs/r_t01_A/steps"} {
		resp := c.do(t, "GET", APIPrefix+path, nil, nil)
		if resp.StatusCode() != 200 {
			t.Fatalf("%s status = %d: %s", path, resp.StatusCode(), resp.Body())
		}
		if strings.Contains(string(resp.Body()), "inventedKEY42abc") {
			t.Fatalf("%s serves the key unmasked: %s", path, resp.Body())
		}
	}
	// The paged and one-step routes read the same masked timeline.
	all, err := readRunSteps(ix, "r_t01_A")
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Steps) != 1 || strings.Contains(all.Steps[0].Summary, "inventedKEY42abc") {
		t.Fatalf("readRunSteps not masked: %+v", all.Steps)
	}
}
