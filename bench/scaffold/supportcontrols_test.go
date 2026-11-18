package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSupportControls runs every control of the support template's sample
// task and of examples/acme-helpdesk on the host, scripted user, no docker
// (testdata/support_controls.py): the free proof that each task's
// reference, mutants, empty and exploit controls and its verifier agree (W3).
func TestSupportControls(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	tmpl, err := Suite(SuiteOptions{ID: "demo", Template: TemplateSupport})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := WriteFiles(dir, tmpl, false); err != nil {
		t.Fatal(err)
	}
	harness, err := filepath.Abs("testdata/support_controls.py")
	if err != nil {
		t.Fatal(err)
	}
	for name, suiteDir := range map[string]string{
		"template":      dir,
		"acme-helpdesk": filepath.Join(benchDir, "examples", "acme-helpdesk", "benchmarks", "acme-helpdesk"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := os.Stat(suiteDir); err != nil {
				t.Skip(err)
			}
			out, err := exec.Command(py, "-B", harness, suiteDir).CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
		})
	}
}

// TestSupportCopiesInStep keeps the copies of the user simulator and the
// helpdesk server in examples/acme-helpdesk identical to their sources: the
// simulator and its prompts to bench/simuser, the server and the verifier to
// the support template (W3).
func TestSupportCopiesInStep(t *testing.T) {
	svc := filepath.Join(benchDir, "examples", "acme-helpdesk", "benchmarks", "acme-helpdesk", "services", "helpdesk")
	if _, err := os.Stat(svc); err != nil {
		t.Skip(err)
	}
	files, err := Suite(SuiteOptions{ID: "demo", Template: TemplateSupport})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Data
	}
	for _, rel := range []string{"simuser.py", "prompts/user-v1.txt", "prompts/tau2-user-v1.txt", "server.py"} {
		got, err := os.ReadFile(filepath.Join(svc, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if string(got) != string(byPath["services/helpdesk/"+rel]) {
			t.Errorf("examples/acme-helpdesk services/helpdesk/%s differs from the template's (copy it from bench/simuser or scaffold/templates/suites/support)", rel)
		}
	}
	verify := byPath["tasks/refund-in-policy/verifier/tests/verify.py"]
	tasks, err := filepath.Glob(filepath.Join(benchDir, "examples", "acme-helpdesk", "benchmarks", "acme-helpdesk", "tasks", "*", "verifier", "tests", "verify.py"))
	if err != nil || len(tasks) != 6 {
		t.Fatalf("verifiers %v %v", tasks, err)
	}
	for _, p := range tasks {
		if got, _ := os.ReadFile(p); string(got) != string(verify) {
			t.Errorf("%s differs from the template's verify.py", p)
		}
	}
}
