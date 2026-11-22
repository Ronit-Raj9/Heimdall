package main

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
)

// N148: the status page lists the evaluation runs line, and the probe list
// adds the model gateway line.
func TestStatusServiceLines(t *testing.T) {
	t.Setenv(StatusFileEnv, t.TempDir()+"/incidents.json")
	t.Setenv(StatusProbesEnv, "gateway=http://127.0.0.1:1/health")
	svc := newStatusService(&lib.Config{Root: t.TempDir()}, nil)
	p := svc.Page(context.Background())
	names := map[string]string{}
	for _, c := range p.Components {
		names[c.ID] = c.Name
	}
	if names["runs"] != "Evaluation runs" || names["gateway"] != "Model gateway" || names["workers"] != "Evaluation workers" {
		t.Fatalf("lines %v", names)
	}
}
