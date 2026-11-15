package environments

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestEventEnv(t *testing.T) {
	rc := &schemas.RunContext{Agent: &schemas.AgentConfig{ID: "my-agent"}}
	off := newDockerWorkspace(DockerWorkspaceConfig{})
	if got := off.eventEnv(rc, "r1", []string{"A=1"}); len(got) != 1 {
		t.Fatalf("collector off added env: %v", got)
	}
	on := newDockerWorkspace(DockerWorkspaceConfig{EventsCollector: true})
	// Collector not serving: only the file channel.
	got := strings.Join(on.eventEnv(rc, "r1", nil), "\n")
	if !strings.Contains(got, "BENCH_EVENTS_FILE="+EventsFilePath) || strings.Contains(got, "OTEL_") || strings.Contains(got, "BENCH_EVENTS_URL") {
		t.Fatalf("no collector: %s", got)
	}
	collectorRuns.Store("r2", true)
	defer collectorRuns.Delete("r2")
	// The agent config's own value wins.
	env := on.eventEnv(rc, "r2", []string{"OTEL_EXPORTER_OTLP_PROTOCOL=grpc", "OTEL_SERVICE_NAME=custom"})
	got = strings.Join(env, "\n")
	for _, want := range []string{
		"BENCH_EVENTS_URL=http://toolproxy:9200/events",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://toolproxy:9200/otlp",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://toolproxy:9200/otlp/v1/traces",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_RESOURCE_ATTRIBUTES=heimdall.run_id=r2",
		"OTEL_EXPORTER_OTLP_PROTOCOL=grpc",
		"OTEL_SERVICE_NAME=custom",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in\n%s", want, got)
		}
	}
	if strings.Count(got, "OTEL_EXPORTER_OTLP_PROTOCOL=") != 1 || strings.Count(got, "OTEL_SERVICE_NAME=") != 1 {
		t.Fatalf("agent value overridden:\n%s", got)
	}
}
