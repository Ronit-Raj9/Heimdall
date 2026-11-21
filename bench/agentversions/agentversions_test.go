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
