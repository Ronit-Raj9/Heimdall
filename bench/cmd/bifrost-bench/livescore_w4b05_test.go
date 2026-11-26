package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/livescore"
)

// p13-9: an unedited template is "not set up yet", not a broken file.
func TestStatusTemplateIsNotAProblem(t *testing.T) {
	config, _ := liveProject(t)
	ctx := context.Background()
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text, err := livescore.Template(livescore.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(a.cfg.Root, livescore.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, livescore.ConfigFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := serveLivescore(a, false).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Configured || st.Problem != "" {
		t.Fatalf("template status: configured=%v problem=%q, want false and empty", st.Configured, st.Problem)
	}
	if st.Note != "The settings still have placeholder keys. Open Set up live scoring and choose how to tell the versions apart." {
		t.Fatalf("note %q", st.Note)
	}
}
