package utils

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestScenarioDefaults(t *testing.T) {
	plain := &schemas.Suite{Workdir: "/work"}
	task := &schemas.Task{Controls: schemas.TaskControls{Reference: "controls/reference.diff"}}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"output default", OutputKind(plain, task), schemas.OutputDiff},
		{"answer path default", AnswerPath(plain, task), DefaultAnswerPath},
		{"answer container path", AnswerContainerPath(plain, task), "/work/answer.txt"},
		{"verifier mode default", VerifierMode(plain), schemas.VerifierCleanRoom},
		{"grading default", Grading(plain), schemas.GradingVerified},
		{"track default", Track(plain), schemas.TrackCoding},
		{"control default", ControlKind(plain, task), ControlPatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestOutputKindPrecedence(t *testing.T) {
	s := &schemas.Suite{Output: &schemas.OutputSpec{Kind: schemas.OutputState, AnswerPath: "out/a.txt"}}
	tests := []struct {
		name     string
		task     *schemas.Task
		wantKind string
		wantPath string
		wantCtl  string
	}{
		{"suite applies", &schemas.Task{Controls: schemas.TaskControls{Reference: "controls/ref.sh"}}, schemas.OutputState, "out/a.txt", ControlScript},
		{"task overrides", &schemas.Task{Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer, AnswerPath: "x.txt"}}, schemas.OutputAnswer, "x.txt", ControlAnswer},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := OutputKind(s, tc.task); got != tc.wantKind {
				t.Errorf("kind %q, want %q", got, tc.wantKind)
			}
			if got := AnswerPath(s, tc.task); got != tc.wantPath {
				t.Errorf("answer path %q, want %q", got, tc.wantPath)
			}
			if got := ControlKind(s, tc.task); got != tc.wantCtl {
				t.Errorf("control %q, want %q", got, tc.wantCtl)
			}
		})
	}
}

func TestServicesAndMCP(t *testing.T) {
	no := false
	s := &schemas.Suite{Services: []schemas.ServiceSpec{{ID: "db", Role: schemas.ServiceRoleData, Export: []schemas.StateExport{{ID: "dump"}}}}}
	task := &schemas.Task{Services: []schemas.ServiceSpec{{ID: "tools", Role: schemas.ServiceRoleMCP}, {ID: "raw", Role: schemas.ServiceRoleTool, Proxy: &no}}}
	all := TaskServices(s, task)
	if len(all) != 3 || all[0].ID != "db" || all[2].ID != "raw" {
		t.Fatalf("services %+v", all)
	}
	if ServiceProxied(all[0]) || !ServiceProxied(all[1]) || ServiceProxied(all[2]) {
		t.Errorf("proxied defaults wrong")
	}
	if !HasExports(all) || HasExports(task.Services) {
		t.Errorf("HasExports wrong")
	}
	handles := []schemas.ServiceHandle{
		{Spec: all[1], URL: "http://toolproxy:9200/s/tools"},
		{Spec: schemas.ServiceSpec{ID: "m2", Role: schemas.ServiceRoleMCP, MCP: &schemas.MCPEndpoint{Path: "/rpc"}}, URL: "http://m2:8000/rpc"},
		{Spec: schemas.ServiceSpec{ID: "hid", Role: schemas.ServiceRoleMCP, Hidden: true}, URL: "http://hid:1"},
		{Spec: all[0], URL: "http://db:5432"},
	}
	got, err := MCPServersJSON(handles)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"m2":{"url":"http://m2:8000/rpc"},"tools":{"url":"http://toolproxy:9200/s/tools/mcp"}}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
