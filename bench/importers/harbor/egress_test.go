package harbor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestImportEgressRecord(t *testing.T) {
	from := filepath.Join("testdata", "tb")
	root, res := runImport(t, from, "s", importers.Options{IDs: []string{"fetching-solution", "hello-file", "downloads-tests"}, Extra: map[string]string{SetEgress: "record"}})
	if strings.Join(res.Tasks, ",") != "fetching-solution,hello-file,downloads-tests" || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v", res)
	}
	s := loadSuite(t, root, "s")
	// A test script that downloads: the verifier reaches the egress service.
	if dt := s.TaskByID("downloads-tests"); len(dt.Services) != 1 || !dt.Services[0].Verifier {
		t.Fatalf("downloads-tests services = %+v", dt.Services)
	}
	task := s.TaskByID("fetching-solution")
	if len(task.Services) != 1 || task.Services[0].Role != schemas.ServiceRoleEgress || task.Services[0].Port != egress.Port || task.Services[0].Verifier || !contains(task.Tags, EgressTag) {
		t.Fatalf("task services = %+v tags %v", task.Services, task.Tags)
	}
	dir := filepath.Join(root, "benchmarks/s/tasks/fetching-solution")
	pemBytes := readFile(t, filepath.Join(dir, egress.TaskCAFile))
	if _, err := egress.LoadCA([]byte(pemBytes)); err != nil {
		t.Fatalf("task CA: %v", err)
	}
	crt := readFile(t, filepath.Join(dir, "environment", egress.ImageCAFile))
	if strings.Contains(crt, "PRIVATE KEY") || !strings.Contains(crt, "CERTIFICATE") {
		t.Fatal("the build context holds the CA key or no certificate")
	}
	if c, err := egress.OpenCache(filepath.Join(dir, egress.TaskCacheFile)); err != nil || len(c.Entries()) != 0 {
		t.Fatalf("empty cache: %v", err)
	}
	df := readFile(t, filepath.Join(dir, "environment/Dockerfile"))
	if !strings.Contains(df, "COPY "+egress.ImageCAFile+" "+egress.CAPath) || strings.Index(df, egress.CAPath) > strings.LastIndex(df, "USER ") {
		t.Errorf("Dockerfile:\n%s", df)
	}
	if notes := readFile(t, filepath.Join(dir, "IMPORT.md")); !strings.Contains(notes, "Recorded internet") {
		t.Errorf("IMPORT.md:\n%s", notes)
	}
	// A task that needs no download is unchanged.
	if hf := s.TaskByID("hello-file"); len(hf.Services) != 0 {
		t.Errorf("hello-file services = %+v", hf.Services)
	}
	if _, err := os.Stat(filepath.Join(root, "benchmarks/s/tasks/hello-file/egress")); err == nil {
		t.Error("hello-file got an egress folder")
	}
	if _, err := readSettings(map[string]string{SetEgress: "maybe"}); err == nil {
		t.Error("egress=maybe accepted")
	}
}
