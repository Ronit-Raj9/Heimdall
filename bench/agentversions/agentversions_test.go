package agentversions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const agentDoc = `id: exploit
adapter: scripted
image: bench/coding-core-base:1
scripted:
  kind: exploit
model: claude-sonnet-4-5
budgets:
  wall_s: 900
  cpus: 1
  mem_gb: 1
gateway:
  provider: anthropic
`

func TestSnapshotDedupesByHash(t *testing.T) {
	root := t.TempDir()
	v, created, err := Snapshot(root, "exploit", []byte(agentDoc), "ana", "first")
	if err != nil || !created || v.Version != 1 || v.Hash == "" || v.CreatedBy != "ana" || v.Note != "first" {
		t.Fatalf("v1: %+v %v %v", v, created, err)
	}
	// A comment and reordering do not change the config hash.
	same := "# a comment\n" + agentDoc
	if v2, created, err := Snapshot(root, "exploit", []byte(same), "bo", ""); err != nil || created || v2.Version != 1 {
		t.Fatalf("same hash made a version: %+v %v %v", v2, created, err)
	}
	changed := strings.Replace(agentDoc, "wall_s: 900", "wall_s: 600", 1)
	v2, created, err := Snapshot(root, "exploit", []byte(changed), "bo", "")
	if err != nil || !created || v2.Version != 2 || v2.Hash == v.Hash {
		t.Fatalf("v2: %+v %v %v", v2, created, err)
	}
	// Back to the first config is a new version: only the latest is compared.
	if v3, created, _ := Snapshot(root, "exploit", []byte(agentDoc), "", ""); !created || v3.Version != 3 || v3.Hash != v.Hash {
		t.Fatalf("v3: %+v %v", v3, created)
	}
	vs, err := List(root, "exploit")
	if err != nil || len(vs) != 3 {
		t.Fatalf("list: %+v %v", vs, err)
	}
	doc, e, err := Read(root, "exploit", 2)
	if err != nil || string(doc) != changed || e.Version != 2 {
		t.Fatalf("read v2: %q %+v %v", doc, e, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".heimdall", "agent-versions", "exploit", "versions.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(root, "exploit", 9); err != ErrNotFound {
		t.Fatalf("missing version: %v", err)
	}
}

func TestSnapshotRefusesBadInput(t *testing.T) {
	root := t.TempDir()
	if _, _, err := Snapshot(root, "../x", []byte(agentDoc), "", ""); err == nil {
		t.Fatal("bad id accepted")
	}
	if _, _, err := Snapshot(root, "x", []byte("id: x\nnope: 1\n"), "", ""); err == nil {
		t.Fatal("invalid agent accepted")
	}
	if vs, err := List(root, "x"); err != nil || len(vs) != 0 {
		t.Fatalf("list after refusal: %+v %v", vs, err)
	}
}

func TestLibraryVersion(t *testing.T) {
	lib := t.TempDir()
	os.MkdirAll(filepath.Join(lib, "agents"), 0o755)
	os.WriteFile(filepath.Join(lib, "agents", "exploit.yaml"), []byte(agentDoc), 0o644)
	p := LibraryFile(lib, "exploit")
	if p == "" || LibraryFile(lib, "nope") != "" || LibraryFile("", "exploit") != "" {
		t.Fatalf("library file %q", p)
	}
	doc, v, err := LibraryVersion(p)
	h, _ := ConfigHash([]byte(agentDoc))
	if err != nil || v.Version != 1 || v.CreatedBy != LibraryActor || v.Hash != h || string(doc) != agentDoc {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestRecordBackfillsFromHistory(t *testing.T) {
	root := t.TempDir()
	changed := strings.Replace(agentDoc, "wall_s: 900", "wall_s: 600", 1)
	hist := []Source{
		{Doc: []byte(agentDoc), FileVersion: "f1", Actor: "ana"},
		{Doc: []byte("not: an agent\n"), FileVersion: "f2"},
		{Doc: []byte("# same\n" + agentDoc), FileVersion: "f3"},
		{Doc: []byte(changed), FileVersion: "f4"},
	}
	v, created, err := Record(root, "exploit", hist)
	if err != nil || !created || v.Version != 2 || v.FileVersion != "f4" {
		t.Fatalf("%+v %v %v", v, created, err)
	}
	vs, _ := List(root, "exploit")
	if len(vs) != 2 || vs[0].FileVersion != "f1" || vs[0].CreatedBy != "ana" {
		t.Fatalf("%+v", vs)
	}
	// With versions present only the last entry counts: no re-backfill.
	if v, created, _ := Record(root, "exploit", hist[:1]); !created || v.Version != 3 || v.FileVersion != "f1" {
		t.Fatalf("%+v %v", v, created)
	}
}

// TestAgentHashIgnoresArmAxes (N6): a run of a version on any model and
// provider, and the hidden repeat arm, hash as the version's snapshot; a
// change to the agent itself does not.
func TestAgentHashIgnoresArmAxes(t *testing.T) {
	base, err := DocAgentHash([]byte(agentDoc))
	if err != nil || base == "" {
		t.Fatalf("doc agent hash: %q %v", base, err)
	}
	cfg, err := decodeConfig([]byte(agentDoc))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model, cfg.Gateway.Provider = "gpt-5.4-mini", "openai"
	cfg.Env = map[string]string{RepeatArmEnv: "B"}
	if h, _ := AgentHash(cfg); h != base {
		t.Errorf("arm with model, provider and repeat marker = %s, want %s", h, base)
	}
	cfg.Budgets.WallS = 60
	if h, _ := AgentHash(cfg); h == base {
		t.Error("a budget change kept the agent hash")
	}

	root := t.TempDir()
	v1, _, err := Snapshot(root, "exploit", []byte(agentDoc), "", "")
	if err != nil || v1.AgentHash != base {
		t.Fatalf("v1 = %+v %v", v1, err)
	}
	// A new default model alone is no new version.
	other := strings.Replace(agentDoc, "claude-sonnet-4-5", "claude-opus-4-1", 1)
	if v, created, _ := Snapshot(root, "exploit", []byte(other), "", ""); created || v.Version != 1 {
		t.Fatalf("model-only change made %+v (created %v)", v, created)
	}
	changed := strings.Replace(agentDoc, "wall_s: 900", "wall_s: 600", 1)
	v2, _, _ := Snapshot(root, "exploit", []byte(changed), "", "")
	if got := VersionOf(root, "", "exploit", base); got != 1 {
		t.Errorf("VersionOf(v1 hash) = %d", got)
	}
	if got := VersionOf(root, "", "exploit", "sha256:"+strings.TrimPrefix(v2.AgentHash, "sha256:")); got != 2 {
		t.Errorf("VersionOf(v2 hash) = %d", got)
	}
	if got := VersionOf(root, "", "exploit", "nope"); got != 0 {
		t.Errorf("VersionOf(unknown) = %d", got)
	}
	// An index written before agent hashes is filled from the snapshots.
	idx := filepath.Join(root, ".heimdall", "agent-versions", "exploit", "versions.json")
	if err := os.WriteFile(idx, []byte(`[{"version":1,"hash":"x"},{"version":2,"hash":"y"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := VersionOf(root, "", "exploit", base); got != 1 {
		t.Errorf("old index VersionOf = %d", got)
	}
	// A built-in library agent the project does not version is v1.
	lib := t.TempDir()
	os.MkdirAll(filepath.Join(lib, "agents"), 0o755)
	os.WriteFile(filepath.Join(lib, "agents", "builtin.yaml"), []byte(strings.Replace(agentDoc, "id: exploit", "id: builtin", 1)), 0o644)
	bh, _ := DocAgentHash([]byte(strings.Replace(agentDoc, "id: exploit", "id: builtin", 1)))
	if got := VersionOf(root, lib, "builtin", bh); got != 1 {
		t.Errorf("library VersionOf = %d", got)
	}
}

func TestDeriveRecordsLineage(t *testing.T) {
	root := t.TempDir()
	if _, _, err := Snapshot(root, "exploit", []byte(agentDoc), "ana", ""); err != nil {
		t.Fatal(err)
	}
	cand := strings.Replace(agentDoc, "kind: exploit", "kind: exploit\nfiles:\n  /home/agent/AGENTS.md: be careful", 1)
	lin := schemas.AgentVersion{Parent: 1, Author: schemas.AuthorAgent, Improvement: "imp_x", Round: 1, BundleHash: "b1", CreatedBy: "improver"}
	v, created, err := Derive(root, "exploit", []byte(cand), lin)
	if err != nil || !created || v.Version != 2 || v.Parent != 1 || v.Author != schemas.AuthorAgent || v.Round != 1 || v.BundleHash != "b1" {
		t.Fatalf("derive: %+v %v %v", v, created, err)
	}
	// The same doc again returns the first lineage; the parent's doc returns v1.
	if again, created, err := Derive(root, "exploit", []byte(cand), schemas.AgentVersion{Parent: 1, Round: 2}); err != nil || created || again.Round != 1 {
		t.Fatalf("again: %+v %v %v", again, created, err)
	}
	if p, created, err := Derive(root, "exploit", []byte(agentDoc), lin); err != nil || created || p.Version != 1 {
		t.Fatalf("parent doc: %+v %v %v", p, created, err)
	}
	if _, _, err := Derive(root, "exploit", []byte(strings.Replace(cand, "wall_s: 900", "wall_s: 901", 1)), schemas.AgentVersion{Parent: 9}); err == nil {
		t.Fatal("unknown parent accepted")
	}
}
