package scaffold

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata/golden")

// benchDir is the bench root, the parent of this package.
const benchDir = ".."

func TestProject(t *testing.T) {
	files, err := Project(ProjectOptions{Name: "acme-bench"})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]File{}
	for _, f := range files {
		byPath[f.Path] = f
		if strings.Contains(f.Path, "dot-") || strings.HasSuffix(f.Path, ".tmpl") || strings.HasSuffix(f.Path, ".raw") || strings.HasSuffix(f.Path, ".gz") {
			t.Errorf("naming rule not applied to %s", f.Path)
		}
	}
	for _, want := range []string{
		"deploy/bench.yaml", "deploy/docker-compose.yml", "deploy/.env.example", "deploy/Dockerfile", "deploy/.dockerignore",
		"deploy/helper/Dockerfile", "deploy/pricing-snapshot.json", "deploy/bifrost/config.json",
		"deploy/bifrost/datasheet/pricing.json", "deploy/bifrost/datasheet/model-parameters.json",
		"agents/oracle.yaml", "agents/noop.yaml", "agents/refuse.yaml", "agents/exploit.yaml", "agents/toy.yaml",
		"benchmarks/README.md", "experiments/smoke.yaml", "experiments/fixtures/note.md", "policies/default.yaml",
		".gitignore", "README.md",
	} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("project lacks %s", want)
		}
	}
	if !strings.Contains(string(byPath["README.md"].Data), "# acme-bench: Heimdall benchmark project") {
		t.Error("README does not carry the project name")
	}
	for _, want := range []string{"/runs/", "/deploy/.env", "/deploy/bifrost-bench", "/.heimdall-jobs/", "/.heimdall/"} {
		if !strings.Contains(string(byPath[".gitignore"].Data), want+"\n") {
			t.Errorf(".gitignore lacks %s", want)
		}
	}
	if steps := strings.Count(string(byPath["README.md"].Data), "\n1. ") + strings.Count(string(byPath["README.md"].Data), "\n6. "); steps != 2 {
		t.Errorf("README next commands are not numbered 1 to 6")
	}
}

// TestProjectConfigLoads writes the project and loads its bench.yaml, which
// must resolve every path inside the project.
func TestProjectConfigLoads(t *testing.T) {
	dir := t.TempDir()
	files, err := Project(ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFiles(dir, files, false); err != nil {
		t.Fatal(err)
	}
	cfg, err := lib.LoadConfig(filepath.Join(dir, "deploy", "bench.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{cfg.RunsRoot, cfg.BenchmarksRoot, cfg.Pricing} {
		if filepath.IsAbs(p) {
			t.Errorf("bench.yaml path %s is absolute", p)
		}
	}
	if cfg.Root != dir {
		t.Errorf("root = %s, want %s", cfg.Root, dir)
	}
	if _, err := os.Stat(cfg.Path(cfg.Pricing)); err != nil {
		t.Errorf("pricing snapshot: %v", err)
	}
	for _, a := range []string{"oracle", "noop", "refuse", "exploit", "toy"} {
		var ac schemas.AgentConfig
		if err := suite.DecodeYAMLFile(filepath.Join(dir, "agents", a+".yaml"), a, suite.SchemaAgent, &ac); err != nil {
			t.Errorf("agent %s: %v", a, err)
		}
	}
	var exp schemas.Experiment
	if err := suite.DecodeYAMLFile(filepath.Join(dir, "experiments", "smoke.yaml"), "smoke", suite.SchemaExperiment, &exp); err != nil {
		t.Errorf("smoke experiment: %v", err)
	}
	if exp.Policy != "policies/default.yaml" || exp.Base != "agents/toy.yaml" {
		t.Errorf("smoke experiment points at %s and %s", exp.Policy, exp.Base)
	}
}

// TestProjectMatchesRepo keeps the embedded copies in step with the files
// they were taken from, so a fresh project behaves as the bench repository.
func TestProjectMatchesRepo(t *testing.T) {
	files, err := Project(ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]byte{}
	for _, f := range files {
		got[f.Path] = f.Data
	}
	same := map[string]string{
		"deploy/.env.example":                            "deploy/.env.example",
		"deploy/pricing-snapshot.json":                   "deploy/pricing-snapshot.json",
		"deploy/bifrost/config.json":                     "deploy/bifrost/config.json",
		"deploy/bifrost/datasheet/pricing.json":          "deploy/bifrost/datasheet/pricing.json",
		"deploy/bifrost/datasheet/model-parameters.json": "deploy/bifrost/datasheet/model-parameters.json",
		"agents/oracle.yaml":                             "agents/oracle.yaml",
		"agents/noop.yaml":                               "agents/noop.yaml",
		"agents/refuse.yaml":                             "agents/refuse.yaml",
		"agents/exploit.yaml":                            "agents/exploit.yaml",
		"agents/toy.yaml":                                "agents/toy.yaml",
		"policies/default.yaml":                          "policies/default.yaml",
	}
	for p, src := range same {
		want := readRepo(t, src)
		if !bytes.Equal(got[p], want) {
			t.Errorf("%s differs from bench/%s; copy it again (gzip -9 -n for the datasheets)", p, src)
		}
	}
	tails := []struct{ project, repo, from string }{
		{"deploy/docker-compose.yml", "deploy/docker-compose.yml", "# Networks:"},
		{"deploy/helper/Dockerfile", "benchmarks/coding-core/base/Dockerfile", "FROM "},
		{"deploy/Dockerfile", "Dockerfile", "RUN addgroup"},
	}
	for _, tc := range tails {
		g, w := tailFrom(string(got[tc.project]), tc.from), tailFrom(string(readRepo(t, tc.repo)), tc.from)
		if tc.project == "deploy/Dockerfile" {
			w = strings.Replace(w, "COPY bin/bifrost-bench", "COPY bifrost-bench", 1)
		}
		if g == "" || g != w {
			t.Errorf("%s differs from bench/%s after %q", tc.project, tc.repo, tc.from)
		}
	}
	var gotCfg, wantCfg map[string]any
	if err := yaml.Unmarshal(got["deploy/bench.yaml"], &gotCfg); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(readRepo(t, "deploy/bench.yaml"), &wantCfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotCfg, wantCfg) {
		t.Errorf("deploy/bench.yaml values differ from bench/deploy/bench.yaml:\n%v\n%v", gotCfg, wantCfg)
	}
}

func TestProjectDefault(t *testing.T) {
	b, err := ProjectDefault("deploy/pricing-snapshot.json")
	if err != nil || !bytes.Equal(b, readRepo(t, "deploy/pricing-snapshot.json")) {
		t.Fatalf("pricing default: %v", err)
	}
	if _, err := ProjectDefault("nope.txt"); err == nil {
		t.Fatal("want error for an unknown default")
	}
}

func TestSuite(t *testing.T) {
	for _, tmpl := range SuiteTemplates() {
		t.Run(tmpl, func(t *testing.T) {
			files, err := Suite(SuiteOptions{ID: "demo", Template: tmpl})
			if err != nil {
				t.Fatal(err)
			}
			byPath := map[string][]byte{}
			for _, f := range files {
				byPath[f.Path] = f.Data
			}
			for _, want := range []string{"suite.yaml", "base/Dockerfile", "README.md", "probes/model-outage.yaml"} {
				if _, ok := byPath[want]; !ok {
					t.Errorf("missing %s", want)
				}
			}
			if DetectTemplate(byPath["suite.yaml"]) != tmpl {
				t.Errorf("suite.yaml does not name template %s", tmpl)
			}
			probes := []string{"model-outage"}
			switch {
			case tmpl == TemplateBrowser:
				probes = append(probes, "browser-error-first", "browser-slow", "hostile-banner")
			case tmpl == TemplateExternal:
				probes = append(probes, "fx-error-first")
			case tmpl == TemplateComputerUse:
				probes = append(probes, "screenshot-error", "hostile-dialog")
			case tmpl == TemplateSupport: // W3 customer support
				probes = append(probes, "tool-error-first", "hostile-customer")
			case tmpl == TemplateResearch: // W4 research with Tavily
				probes = append(probes, "search-outage", "stale-results", "hostile-page")
			case tmpl == TemplateVoice: // X8 voice agents
				probes = append(probes, "caller-interrupts", "caller-noise", "caller-silent", "caller-speed", "stt-outage")
			case stateTemplate(tmpl):
				probes = append(probes, "tool-error")
			}
			validSuite(t, byPath["suite.yaml"], "bench/demo-base:1", true, probes...)
			if !bytes.Contains(byPath["base/Dockerfile"], []byte("-u 1000")) && !bytes.Contains(byPath["base/Dockerfile"], []byte("usermod -l agent")) {
				t.Error("base/Dockerfile does not set up the uid 1000 user")
			}
			if !bytes.Equal(byPath["probes/model-outage.yaml"], readRepo(t, "benchmarks/coding-core/probes/model-outage.yaml")) {
				t.Error("model-outage probe differs from coding-core's")
			}
			if IsScenarioTemplate(tmpl) {
				goldenManifest(t, "suite-"+tmpl, files)
				return
			}
			golden(t, "suite-"+tmpl, files)
		})
	}
}

func TestSuitePythonBaseMatchesCodingCore(t *testing.T) {
	files, err := Suite(SuiteOptions{ID: "demo", Template: TemplatePythonPytest})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "base/Dockerfile" && tailFrom(string(f.Data), "FROM ") != tailFrom(string(readRepo(t, "benchmarks/coding-core/base/Dockerfile")), "FROM ") {
			t.Error("python base/Dockerfile differs from coding-core's")
		}
	}
}

func TestSuiteExistingImage(t *testing.T) {
	files, err := Suite(SuiteOptions{ID: "demo", Template: TemplateNodeTest, Image: "acme/node:22"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "base/") {
			t.Errorf("wrote %s with an existing image", f.Path)
		}
		if f.Path == "suite.yaml" {
			validSuite(t, f.Data, "acme/node:22", false)
		}
	}
}

func TestSuiteErrors(t *testing.T) {
	if _, err := Suite(SuiteOptions{ID: "Bad ID", Template: TemplateGoTest}); err == nil {
		t.Error("want error for a bad id")
	}
	if _, err := Suite(SuiteOptions{ID: "ok", Template: "ruby-rspec"}); err == nil || !strings.Contains(err.Error(), "python-pytest") {
		t.Errorf("want error naming the templates, got %v", err)
	}
}

func TestWriteFiles(t *testing.T) {
	dir := t.TempDir()
	files := []File{
		{Path: "a/run.sh", Data: []byte("#!/bin/sh\n"), Mode: 0o755},
		{Path: "a/b.txt", Data: []byte("b")},
		{Path: "a/link", Link: "b.txt"},
	}
	if err := WriteFiles(dir, files, false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "a/run.sh")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("run.sh mode: %v %v", fi, err)
	}
	if target, err := os.Readlink(filepath.Join(dir, "a/link")); err != nil || target != "b.txt" {
		t.Errorf("link = %q, %v", target, err)
	}
	if err := WriteFiles(dir, files[1:2], false); err == nil {
		t.Error("want error writing over an existing file")
	}
	if err := WriteFiles(dir, []File{{Path: "a/b.txt", Data: []byte("new")}}, true); err != nil {
		t.Errorf("overwrite: %v", err)
	}
	for _, bad := range []string{"../x", "/etc/x", "a/../../x", ""} {
		if err := WriteFiles(dir, []File{{Path: bad}}, true); err == nil {
			t.Errorf("want error for path %q", bad)
		}
	}
}

func TestRenderFile(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte("unzipped")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		rel, raw, wantPath, wantData string
		wantMode                     os.FileMode
	}{
		{"deploy/dot-env.example", "A=1", "deploy/.env.example", "A=1", 0o644},
		{"README.md.tmpl", "# [[.Name]] {{keep}}", "README.md", "# demo {{keep}}", 0o644},
		{"gojunit/main.go.raw", "package main [[x]]", "gojunit/main.go", "package main [[x]]", 0o644},
		{"data.json.gz", gz.String(), "data.json", "unzipped", 0o644},
		{"controls/exploit.sh", "#!/bin/sh", "controls/exploit.sh", "#!/bin/sh", 0o755},
	}
	for _, tc := range tests {
		f, err := renderFile(tc.rel, []byte(tc.raw), map[string]any{"Name": "demo"})
		if err != nil {
			t.Fatalf("%s: %v", tc.rel, err)
		}
		if f.Path != tc.wantPath || string(f.Data) != tc.wantData || f.Mode != tc.wantMode {
			t.Errorf("%s = %s %q %v", tc.rel, f.Path, f.Data, f.Mode)
		}
	}
	if _, err := renderFile("x.tmpl", []byte("[[.Missing]]"), map[string]any{}); err == nil {
		t.Error("want error for a missing template key")
	}
}

// validSuite checks a generated suite.yaml against the suite schema once a
// task is listed (the schema wants at least one). probes defaults to
// model-outage alone.
func validSuite(t *testing.T, raw []byte, image string, dockerfile bool, probes ...string) {
	t.Helper()
	listed, _, err := AddTaskID(raw, "t01")
	if err != nil {
		t.Fatal(err)
	}
	var s schemas.Suite
	if err := suite.DecodeYAML(listed, "suite.yaml", suite.SchemaSuite, &s); err != nil {
		t.Fatalf("suite.yaml: %v", err)
	}
	if s.ID != "demo" || s.BaseImage.Tag != image || (s.BaseImage.Dockerfile != "") != dockerfile || s.Workdir != "/workspace" {
		t.Errorf("suite = %+v", s)
	}
	if len(probes) == 0 {
		probes = []string{"model-outage"}
	}
	if !reflect.DeepEqual(s.ProbeIDs, probes) {
		t.Errorf("probes = %v, want %v", s.ProbeIDs, probes)
	}
}

// golden compares files with testdata/golden/<name>/, or rewrites them with
// -update.
func golden(t *testing.T, name string, files []File) {
	t.Helper()
	dir := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := WriteFiles(dir, files, true); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, f := range files {
		want, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Errorf("golden %s/%s: %v (run go test ./scaffold -update)", name, f.Path, err)
			continue
		}
		if !bytes.Equal(f.Data, want) {
			t.Errorf("%s/%s differs from its golden file (run go test ./scaffold -update and review the diff)", name, f.Path)
		}
	}
}

// TestProjectGolden pins the project scaffold by a manifest of path, mode
// and sha256, so the large embedded files are not copied into testdata.
func TestProjectGolden(t *testing.T) {
	files, err := Project(ProjectOptions{Name: "golden"})
	if err != nil {
		t.Fatal(err)
	}
	goldenManifest(t, "project", files)
}

// goldenManifest pins files by a manifest of path, mode and sha256.
func goldenManifest(t *testing.T, name string, files []File) {
	t.Helper()
	var b strings.Builder
	for _, f := range files {
		sum := sha256.Sum256(f.Data)
		fmt.Fprintf(&b, "%s %o %s\n", f.Path, f.Mode, hex.EncodeToString(sum[:]))
	}
	golden(t, name, []File{{Path: "manifest.txt", Data: []byte(b.String()), Mode: 0o644}})
}

func readRepo(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(benchDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read bench/%s: %v", rel, err)
	}
	return b
}

func tailFrom(s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	return s[i:]
}
