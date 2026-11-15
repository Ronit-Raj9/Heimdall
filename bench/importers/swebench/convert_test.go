package swebench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixtureRows reads testdata/rows.jsonl: hand-written rows in the shapes of
// SWE-rebench, SWE-bench-Live, the original SWE-bench, Impossible-SWEbench,
// and three rows that must be skipped.
func fixtureRows(t *testing.T) []row {
	t.Helper()
	raw, err := readRows(filepath.Join("testdata", "rows.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]row, len(raw))
	for i, b := range raw {
		if err := json.Unmarshal(b, &out[i]); err != nil {
			t.Fatalf("row %d: %v", i+1, err)
		}
	}
	return out
}

func TestConvert(t *testing.T) {
	rows := fixtureRows(t)
	cases := []struct {
		name       string
		row        row
		st         settings
		id, image  string
		impossible bool
		cmd        string
		required   []string
		skip       string
	}{
		{"swe-rebench", rows[0], settings{}, "acme--calc-1", "swerebench/sweb.eval.x86_64.acme_1776_calc-1", false,
			"pytest --no-header -rA --tb=line --color=no -p no:cacheprovider tests/test_core.py\n", []string{"fail_to_pass::tests/test_core.py:test_add"}, ""},
		{"swe-bench-live", rows[1], settings{}, "acme--calc-2", "starryzhang/sweb.eval.x86_64.acme_1776_calc-2", false,
			"pip install -e . --no-build-isolation\npytest -rA\n", []string{"fail_to_pass::tests/test_core.py:test_add"}, ""},
		{"original swe-bench", rows[2], settings{}, "django--django-11099", "swebench/sweb.eval.x86_64.django_1776_django-11099:latest", false,
			"./tests/runtests.py --verbosity 2 --settings=test_sqlite --parallel 1 test_core\n", []string{"fail_to_pass::test_add (tests.test_core.CoreTests)"}, ""},
		{"impossible swebench", rows[3], settings{}, "sympy--sympy-20590", "swebench/sweb.eval.x86_64.sympy_1776_sympy-20590:latest", true,
			cmdSympy + " tests/test_core.py\n", []string{"test_command::exit_status"}, ""},
		{"test_cmd setting", rows[0], settings{TestCmd: "python -m pytest -rA"}, "acme--calc-1", "swerebench/sweb.eval.x86_64.acme_1776_calc-1", false,
			"python -m pytest -rA tests/test_core.py\n", []string{"fail_to_pass::tests/test_core.py:test_add"}, ""},
		{"null image", rows[4], settings{}, "acme--calc-3", "", false, "", nil, "published no image"},
		{"unsupported parser", rows[5], settings{}, "acme--calc-4", "", false, "", nil, "parse_log_cargo"},
		{"leaky statement", rows[6], settings{}, "acme--calc-5", "", false, "", nil, "mentions \"grader\""},
		{"bad rule", rows[0], settings{Rule: "vibes"}, "acme--calc-1", "", false, "", nil, "rule \"vibes\""},
		{"unknown repo without command", func() row { r := rows[2]; r.Repo = "acme/other"; return r }(), settings{}, "django--django-11099", "", false, "", nil, "pass -set test_cmd"},
		{"bad base commit", func() row { r := rows[0]; r.BaseCommit = "main"; return r }(), settings{}, "acme--calc-1", "", false, "", nil, "not a commit hash"},
		{"no fail to pass", func() row { r := rows[0]; r.FailToPass = nil; return r }(), settings{}, "acme--calc-1", "", false, "", nil, "FAIL_TO_PASS is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := convert(c.row, c.st)
			if got.ID != c.id {
				t.Fatalf("id %q, want %q", got.ID, c.id)
			}
			if c.skip != "" {
				if !strings.Contains(got.Skip, c.skip) {
					t.Fatalf("skip %q, want it to contain %q", got.Skip, c.skip)
				}
				return
			}
			if got.Skip != "" {
				t.Fatalf("skipped: %s", got.Skip)
			}
			if got.Image != c.image || got.Impossible != c.impossible || got.Workspace != defaultWorkspace {
				t.Fatalf("image %q impossible %v workspace %q", got.Image, got.Impossible, got.Workspace)
			}
			if cmd := got.Files["verifier/tests/cmd.sh"]; !strings.HasSuffix(cmd, "\n"+c.cmd) {
				t.Errorf("cmd.sh %q, want it to end with %q", cmd, c.cmd)
			}
			y := got.Files["task.yaml"]
			for _, req := range c.required {
				if !strings.Contains(y, req) {
					t.Errorf("task.yaml lacks required %q:\n%s", req, y)
				}
			}
			for _, want := range []string{"image: " + c.image, "workspace_path: /testbed", "reference: controls/reference.sh", "empty: controls/empty.sh"} {
				if !strings.Contains(y, want) {
					t.Errorf("task.yaml lacks %q", want)
				}
			}
			if strings.Contains(y, "impossible: true") != c.impossible {
				t.Errorf("impossible flag in task.yaml wrong")
			}
			_, hasOriginal := got.Files["verifier/original/test.patch"]
			if hasOriginal != c.impossible {
				t.Errorf("verifier/original present=%v, want %v", hasOriginal, c.impossible)
			}
		})
	}
}

func TestConvertFiles(t *testing.T) {
	got := convert(fixtureRows(t)[0], settings{})
	if got.Files["verifier/tests/test_files"] != "tests/test_core.py\ntests/data/cases.json\n" {
		t.Errorf("test_files %q", got.Files["verifier/tests/test_files"])
	}
	if !strings.Contains(got.Files["task.yaml"], "protected:\n    - tests/test_core.py\n") {
		t.Errorf("protected should hold the modified test file only:\n%s", got.Files["task.yaml"])
	}
	if !strings.Contains(got.Files["task.yaml"], "license: MIT") || !strings.Contains(got.Files["task.yaml"], "imported_from: swe-rebench acme__calc-1") {
		t.Errorf("provenance:\n%s", got.Files["task.yaml"])
	}
	var spec testSpec
	if err := json.Unmarshal([]byte(got.Files["verifier/tests/tests.json"]), &spec); err != nil {
		t.Fatal(err)
	}
	want := testSpec{Instance: "acme__calc-1", Rule: ruleTests, LogParser: parserPytest, Tests: []specTest{
		{Kind: kindF2P, ID: "tests/test_core.py::test_add", Name: "tests/test_core.py:test_add"},
		{Kind: kindP2P, ID: "tests/test_core.py::test_zero", Name: "tests/test_core.py:test_zero"},
	}}
	if !reflect.DeepEqual(spec, want) {
		t.Errorf("tests.json %+v, want %+v", spec, want)
	}
	if got.Files["verifier/tests/base_commit"] != "0123456789abcdef0123456789abcdef01234567\n" {
		t.Errorf("base_commit %q", got.Files["verifier/tests/base_commit"])
	}
	if !strings.Contains(got.Files["instruction.md"], "add() subtracts instead of adding.") {
		t.Errorf("instruction %q", got.Files["instruction.md"])
	}
}

func TestConvertImpossibleOriginal(t *testing.T) {
	got := convert(fixtureRows(t)[3], settings{Impossible: true})
	if !strings.Contains(got.Files["verifier/tests/test.patch"], "== 6") || !strings.Contains(got.Files["verifier/original/test.patch"], "== 5") {
		t.Fatal("verifier/tests must hold the mutated test patch and verifier/original the upstream one")
	}
	r := fixtureRows(t)[3]
	r.ImpossibleType = "original"
	r.TestPatch = r.OriginalTestPatch
	if got := convert(r, settings{Impossible: true}); got.Impossible || got.Skip != "" {
		t.Fatalf("an original row is a possible task: impossible=%v skip=%q", got.Impossible, got.Skip)
	}
}

func TestPatchScriptApplies(t *testing.T) {
	patch := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-BENCH_GOLD_PATCH\n+x\n"
	s := patchScript(patch)
	if !strings.Contains(s, "<<'BENCH_GOLD_PATCH_0'\n"+patch+"BENCH_GOLD_PATCH_0\n") {
		t.Fatalf("delimiter must avoid the patch text:\n%s", s)
	}
}

func TestStrList(t *testing.T) {
	cases := map[string][]string{`["a", "b"]`: {"a", "b"}, `"[\"a\"]"`: {"a"}, `null`: nil, `""`: nil}
	for in, want := range cases {
		var l strList
		if err := json.Unmarshal([]byte(in), &l); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !reflect.DeepEqual([]string(l), want) {
			t.Errorf("%s: got %v, want %v", in, l, want)
		}
	}
	var l strList
	if err := json.Unmarshal([]byte(`3`), &l); err == nil {
		t.Error("a number is not a list")
	}
	var c cmdList
	if err := json.Unmarshal([]byte(`["a", "b"]`), &c); err != nil || len(c) != 2 {
		t.Errorf("cmd list %v %v", c, err)
	}
}

func TestJUnitName(t *testing.T) {
	for in, want := range map[string]string{
		"tests/a.py::TestX::test_y[p]": "tests/a.py:TestX:test_y[p]",
		"test_x (mod.Class)":           "test_x (mod.Class)",
		"TestSomething":                "#TestSomething",
		"a:::b":                        "a:b",
	} {
		if got := junitName(in); got != want {
			t.Errorf("junitName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChangedAndNewFiles(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "rows.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var r row
	if err := json.Unmarshal([]byte(strings.SplitN(string(b), "\n", 2)[0]), &r); err != nil {
		t.Fatal(err)
	}
	if got := changedFiles(r.TestPatch); !reflect.DeepEqual(got, []string{"tests/test_core.py", "tests/data/cases.json"}) {
		t.Errorf("changed %v", got)
	}
	if got := newFiles(r.TestPatch); !reflect.DeepEqual(got, map[string]bool{"tests/data/cases.json": true}) {
		t.Errorf("new %v", got)
	}
}
