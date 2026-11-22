package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers/hfsource"
)

func TestPickSplit(t *testing.T) {
	splits := []hfsource.Split{
		{Dataset: "o/d", Config: "default", Split: "train"},
		{Dataset: "o/d", Config: "default", Split: "validation"},
		{Dataset: "o/d", Config: "other", Split: "test"},
	}
	if s, err := pickSplit(splits, "", ""); err != nil || s.Split != "validation" {
		t.Fatalf("default: %+v %v", s, err)
	}
	if s, err := pickSplit(splits, "other", ""); err != nil || s.Split != "test" {
		t.Fatalf("other: %+v %v", s, err)
	}
	if _, err := pickSplit(splits, "default", "test"); err == nil || !strings.Contains(err.Error(), "train, validation") {
		t.Fatalf("missing split: %v", err)
	}
	if _, err := pickSplit(nil, "", ""); err == nil {
		t.Fatal("no splits accepted")
	}
}

func TestHarborFromRefKeepsPaths(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bfcl@1.0") // a real folder named like a ref stays a path
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := harborFromRef(p, dir); err != nil || got != p {
		t.Fatalf("existing path: %q %v", got, err)
	}
	if got, err := harborFromRef("datasets/tb2", dir); err != nil || got != "datasets/tb2" {
		t.Fatalf("plain path: %q %v", got, err)
	}
	// A fetched folder is reused without the network.
	cached := filepath.Join(dir, "datasets", "harbor", "bfcl@1.0")
	_ = os.MkdirAll(cached, 0o755)
	_ = os.WriteFile(filepath.Join(cached, "harbor-dataset.json"), []byte(`{"benchmark":"bfcl@1.0"}`), 0o644)
	if got, err := harborFromRef("bfcl@1.0", dir); err != nil || got != cached {
		t.Fatalf("cached ref: %q %v", got, err)
	}
}
