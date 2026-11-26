package traceinbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A source written to sources.yaml after the service started shows in List
// (wave 4, p13-2), and a Bifrost source keeps the defaults its first load got.
func TestInboxSourcesReloadAfterWrite(t *testing.T) {
	ib, _ := newTestInbox(t, &Config{Sources: []Source{{Name: "gw", Kind: KindBifrost, URL: "https://gw.example", TokenEnv: "BF_TOKEN", Filters: Filters{Errors: true}}}}, nil)
	path := filepath.Join(t.TempDir(), SourcesFile)
	if err := os.WriteFile(path, []byte("sources:\n  - name: gw\n    kind: bifrost\n    filters:\n      errors: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ib.SourcesPath = path
	svc := NewService(ib)
	list, err := svc.List(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sources) != 1 || list.Sources[0].Name != "gw" {
		t.Fatalf("before the write: %+v", list.Sources)
	}
	text := "sources:\n  - name: gw\n    kind: bifrost\n    filters:\n      errors: true\n  - name: ls\n    kind: langsmith\n    host: https://api.smith.langchain.com\n    api_key_env: LS_KEY\n    project: p1\n    filters:\n      errors: true\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	list, err = svc.List(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sources) != 2 || list.Sources[1].Name != "ls" {
		t.Fatalf("after the write: %+v", list.Sources)
	}
	got := ib.Sources()
	if got[0].URL != "https://gw.example" || got[0].TokenEnv != "BF_TOKEN" {
		t.Fatalf("bifrost defaults lost: %+v", got[0])
	}
	if err := os.WriteFile(path, []byte("sources: [oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	later = later.Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if n := len(ib.Sources()); n != 2 {
		t.Fatalf("a broken file must keep the old list, got %d sources", n)
	}
}
