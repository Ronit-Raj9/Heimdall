package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// checkComputerUseShape checks what the computer-use template's tasks
// declare: a verifier_command suite on the computer_use track graded on
// state, one proxied MCP desktop service seeded under /seed/home whose state
// is exported by the desktop's own dump command, and script controls.
func checkComputerUseShape(t *testing.T, s *schemas.Suite, task *schemas.Task) {
	t.Helper()
	if s.Output == nil || s.Output.Kind != schemas.OutputState || s.Grader != "verifier_command" ||
		s.Verifier.Result != schemas.VerifierResultReward || s.Track != schemas.TrackComputerUse {
		t.Errorf("suite output %+v grader %s result %s track %s", s.Output, s.Grader, s.Verifier.Result, s.Track)
	}
	if len(task.Services) != 1 {
		t.Fatalf("services = %+v", task.Services)
	}
	d := task.Services[0]
	if d.ID != "desktop" || d.Role != schemas.ServiceRoleMCP || d.Hidden || d.Image != "bench/demo-desktop:1" ||
		d.Port != 8000 || d.MCP == nil || d.MCP.Path != "/mcp" || (d.Proxy != nil && !*d.Proxy) {
		t.Errorf("desktop service = %+v", d)
	}
	if d.Env["DESKTOP_APP"] != "editor" || d.Env["DESKTOP_OPEN"] != "/home/desk/Documents/settings.conf" {
		t.Errorf("desktop env = %v", d.Env)
	}
	if d.Files["/seed/home/Documents/settings.conf"] != "seed/Documents/settings.conf" {
		t.Errorf("desktop files = %v", d.Files)
	}
	if len(d.Export) != 1 || d.Export[0].ID != "state.json" || strings.Join(d.Export[0].Argv, " ") != "python3 -I /opt/desktop/desktop.py dump" {
		t.Errorf("desktop export = %+v", d.Export)
	}
	if d.Ready == nil || strings.Join(d.Ready.Argv, " ") != "python3 -I /opt/desktop/desktop.py ready" {
		t.Errorf("desktop ready = %+v", d.Ready)
	}
	if !strings.HasSuffix(task.Controls.Reference, ".sh") || !strings.HasSuffix(task.Controls.Empty, ".sh") {
		t.Errorf("controls = %+v, want scripts", task.Controls)
	}
}

// TestComputerUseTemplateFiles checks the files only the computer-use
// template writes: the desktop service, its probes, the shared state
// verifier in the sample task, and no inventory leftovers.
func TestComputerUseTemplateFiles(t *testing.T) {
	files, err := Suite(SuiteOptions{ID: "demo", Template: TemplateComputerUse})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	for _, want := range []string{
		"services/desktop/Dockerfile", "services/desktop/desktop.py", "services/desktop/openbox.xml", "services/desktop/test_desktop.py",
		"probes/screenshot-error.yaml", "probes/hostile-dialog.yaml", "tasks/edit-config/verifier/tests/verify.py",
		"tasks/edit-config/verifier/tests/checks.py", "tasks/edit-config/seed/Documents/settings.conf", "README.md",
	} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	for p, f := range byPath {
		if strings.Contains(p, "inventory") || p == "probes/tool-error.yaml" {
			t.Errorf("inventory file %s in a computer-use suite", p)
		}
		if strings.HasSuffix(p, ".sh") && f.Mode != 0o755 {
			t.Errorf("%s mode %o, want 0755", p, f.Mode)
		}
		if bytes.Contains(f.Data, []byte("[[")) && !strings.HasSuffix(p, ".py") {
			t.Errorf("%s holds an unrendered template marker", p)
		}
	}
	if !bytes.Equal(byPath["tasks/edit-config/verifier/tests/verify.py"].Data, readRepo(t, "scaffold/templates/scenario/verify.py")) {
		t.Error("sample verify.py differs from the shared state verifier")
	}
	readme := string(byPath["README.md"].Data)
	for _, want := range []string{"docker build -t bench/demo-desktop:1 benchmarks/demo/services/desktop", "bifrost-bench task admit demo/edit-config"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README.md lacks %q", want)
		}
	}
	tool := string(byPath["probes/screenshot-error.yaml"].Data)
	if !strings.Contains(tool, "layer: tool") || !strings.Contains(tool, "server: desktop") || !strings.Contains(tool, "tool: screenshot") {
		t.Errorf("screenshot-error probe:\n%s", tool)
	}
	dialog := string(byPath["probes/hostile-dialog.yaml"].Data)
	if !strings.Contains(dialog, "layer: service") || !strings.Contains(dialog, "DESKTOP_DIALOG:") {
		t.Errorf("hostile-dialog probe:\n%s", dialog)
	}
}

// TestComputerUseExampleMatchesTemplate keeps the acme-desktop example's
// desktop service and verifier identical to the template's, so a fix in one
// is never lost in the other. Only the Dockerfile's build comment differs.
func TestComputerUseExampleMatchesTemplate(t *testing.T) {
	example := filepath.Join(benchDir, "examples", "acme-desktop", "benchmarks", "acme-desktop")
	if _, err := os.Stat(example); err != nil {
		t.Skip("no acme-desktop example")
	}
	for _, name := range []string{"desktop.py", "openbox.xml", "test_desktop.py"} {
		got := readRepo(t, "examples/acme-desktop/benchmarks/acme-desktop/services/desktop/"+name)
		if !bytes.Equal(got, readRepo(t, "scaffold/templates/suites/computer-use/services/desktop/"+name)) {
			t.Errorf("services/desktop/%s differs between the example and the template", name)
		}
	}
	strip := func(b []byte) []byte {
		var kept [][]byte
		for _, l := range bytes.Split(b, []byte("\n")) {
			if !bytes.HasPrefix(l, []byte("#")) {
				kept = append(kept, l)
			}
		}
		return bytes.Join(kept, []byte("\n"))
	}
	ex := readRepo(t, "examples/acme-desktop/benchmarks/acme-desktop/services/desktop/Dockerfile")
	tm := readRepo(t, "scaffold/templates/suites/computer-use/services/desktop/Dockerfile")
	if !bytes.Equal(strip(ex), strip(tm)) {
		t.Error("services/desktop/Dockerfile differs between the example and the template beyond comments")
	}
	verify := readRepo(t, "scaffold/templates/scenario/verify.py")
	for _, task := range []string{"edit-config", "file-inbox", "expense-form"} {
		if !bytes.Equal(readRepo(t, "examples/acme-desktop/benchmarks/acme-desktop/tasks/"+task+"/verifier/tests/verify.py"), verify) {
			t.Errorf("%s verify.py differs from the shared state verifier", task)
		}
	}
}
