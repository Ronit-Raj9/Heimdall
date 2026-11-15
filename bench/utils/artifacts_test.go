package utils

import (
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestOutputArtifacts(t *testing.T) {
	suiteSpec := []schemas.ArtifactSpec{{ID: "suite", Path: "a.txt"}}
	taskSpec := []schemas.ArtifactSpec{{ID: "task", Path: "b.txt"}}
	cases := []struct {
		name string
		s    *schemas.Suite
		t    *schemas.Task
		want []schemas.ArtifactSpec
	}{
		{"other kind", &schemas.Suite{Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer, Artifacts: suiteSpec}}, &schemas.Task{}, nil},
		{"suite artifacts", &schemas.Suite{Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: suiteSpec}}, &schemas.Task{}, suiteSpec},
		{"task overrides", &schemas.Suite{Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: suiteSpec}},
			&schemas.Task{Output: &schemas.OutputSpec{Artifacts: taskSpec}}, taskSpec},
		{"task kind only", nil, &schemas.Task{Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: taskSpec}}, taskSpec},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OutputArtifacts(c.s, c.t); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCheckArtifactSpec(t *testing.T) {
	cases := []struct {
		name string
		a    schemas.ArtifactSpec
		ok   bool
	}{
		{"plain", schemas.ArtifactSpec{ID: "report", Path: "out/report.csv"}, true},
		{"glob", schemas.ArtifactSpec{ID: "shots", Path: "shots/*.png", MaxBytes: 1 << 20}, true},
		{"bad id", schemas.ArtifactSpec{ID: "Report", Path: "a"}, false},
		{"id with slash", schemas.ArtifactSpec{ID: "a/b", Path: "a"}, false},
		{"empty path", schemas.ArtifactSpec{ID: "a"}, false},
		{"absolute", schemas.ArtifactSpec{ID: "a", Path: "/etc/passwd"}, false},
		{"parent", schemas.ArtifactSpec{ID: "a", Path: "../x"}, false},
		{"unclean", schemas.ArtifactSpec{ID: "a", Path: "x/../y"}, false},
		{"dot", schemas.ArtifactSpec{ID: "a", Path: "."}, false},
		{"recursive glob", schemas.ArtifactSpec{ID: "a", Path: "**/x.png"}, false},
		{"backslash", schemas.ArtifactSpec{ID: "a", Path: `x\y`}, false},
		{"negative cap", schemas.ArtifactSpec{ID: "a", Path: "x", MaxBytes: -1}, false},
		{"cap over total", schemas.ArtifactSpec{ID: "a", Path: "x", MaxBytes: MaxArtifactTotalBytes + 1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := CheckArtifactSpec(c.a); (err == nil) != c.ok {
				t.Fatalf("CheckArtifactSpec(%+v) = %v, want ok %v", c.a, err, c.ok)
			}
		})
	}
}

func TestArtifactIDOK(t *testing.T) {
	for id, want := range map[string]bool{
		"report": true, "chart_1": true, "a-b": true, "0": true,
		"": false, "Report": false, "-a": false, "a/b": false, "..": false, "a.b": false,
	} {
		if got := ArtifactIDOK(id); got != want {
			t.Errorf("ArtifactIDOK(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestArtifactNameOK(t *testing.T) {
	for name, want := range map[string]bool{
		"report.csv": true, "chart_1.svg": true, "a-b.obj": true,
		"": false, ".": false, "..": false, ".hidden": false, "a b": false, "a/b": false, "x\ny": false,
	} {
		if got := ArtifactNameOK(name); got != want {
			t.Errorf("ArtifactNameOK(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestArtifactMaxBytesAndRunPath(t *testing.T) {
	if got := ArtifactMaxBytes(schemas.ArtifactSpec{}); got != DefaultArtifactMaxBytes {
		t.Fatalf("default cap %d", got)
	}
	if got := ArtifactMaxBytes(schemas.ArtifactSpec{MaxBytes: 10}); got != 10 {
		t.Fatalf("cap %d", got)
	}
	if got := ArtifactRunPath("report", "r.csv"); got != "artifacts/report/r.csv" {
		t.Fatalf("run path %s", got)
	}
}

func TestMissingArtifacts(t *testing.T) {
	specs := []schemas.ArtifactSpec{
		{ID: "report", Path: "report.csv", Required: true},
		{ID: "chart", Path: "chart.svg", Required: true},
		{ID: "notes", Path: "notes.md"},
	}
	cases := []struct {
		name  string
		files []schemas.ArtifactFile
		want  []string
	}{
		{"all there", []schemas.ArtifactFile{
			{ID: "report", Path: "artifacts/report/report.csv"},
			{ID: "chart", Path: "artifacts/chart/chart.svg"},
		}, nil},
		{"one missing, optional absent", []schemas.ArtifactFile{
			{ID: "report", Path: "artifacts/report/report.csv"},
			{ID: "chart", Source: "chart.svg", Error: "missing"},
		}, []string{"chart (chart.svg: missing)"}},
		{"over cap", []schemas.ArtifactFile{
			{ID: "report", Source: "report.csv", Error: "over the per-file cap"},
			{ID: "chart", Path: "artifacts/chart/chart.svg"},
		}, []string{"report (report.csv: over the per-file cap)"}},
		{"no entry at all", nil, []string{"report (report.csv: missing)", "chart (chart.svg: missing)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MissingArtifacts(specs, c.files); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
