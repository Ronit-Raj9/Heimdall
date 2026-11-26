package emit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/issuebench"
	_ "github.com/maximhq/bifrost/bench/issuebench/lang/golang"
)

func TestRowCarriesLanguageAndRestore(t *testing.T) {
	v := issuebench.Validation{Candidate: issuebench.Candidate{Repo: "acme/tool", PR: 7, Language: "go"}}
	r := RowOf(v, issuebench.EnvSpec{Language: "go"}, "abc", "img", "")
	if r.Language != "go" || !reflect.DeepEqual(r.Restore, []string{"go.mod", "go.sum"}) {
		t.Fatalf("row language %q restore %v", r.Language, r.Restore)
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"heimdall_language":"go"`) || !strings.Contains(string(b), `"heimdall_restore":["go.mod","go.sum"]`) {
		t.Fatalf("json %s", b)
	}
	py := RowOf(issuebench.Validation{Candidate: issuebench.Candidate{Repo: "a/b", PR: 1}}, issuebench.EnvSpec{Language: "python"}, "abc", "img", "")
	if !reflect.DeepEqual(py.Restore, []string{"conftest.py", "pytest.ini", "tox.ini", "sitecustomize.py", "usercustomize.py"}) {
		t.Fatalf("python restore %v", py.Restore)
	}
}

func TestWriteBuildContextSupportFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ctx")
	if _, err := writeBuildContext(dir, t.TempDir(), Layers{}, "go"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "heimdall", "gotest2junit.go")); err != nil {
		t.Fatalf("converter not in the build context: %v", err)
	}
	py := filepath.Join(t.TempDir(), "ctx")
	if _, err := writeBuildContext(py, t.TempDir(), Layers{}, "python"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(py, "heimdall")); err == nil {
		t.Fatal("python context has a heimdall folder")
	}
}
