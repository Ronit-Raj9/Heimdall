package environments

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// benchCue reports a bench-named token in s.
func benchCue(s string) string {
	low := strings.ToLower(s)
	for _, w := range []string{"bench", "heimdall", "canary", "toolproxy"} {
		if strings.Contains(low, w) {
			return w
		}
	}
	return ""
}

func TestViewFor(t *testing.T) {
	tests := []struct {
		name string
		rc   *schemas.RunContext
		want string
	}{
		{"nil run context", nil, schemas.RealismStandard},
		{"nothing set", &schemas.RunContext{Experiment: &schemas.Experiment{}, Suite: &schemas.Suite{}}, schemas.RealismStandard},
		{"suite neutral", &schemas.RunContext{Suite: &schemas.Suite{Realism: schemas.RealismNeutral}}, schemas.RealismNeutral},
		{"experiment overrides suite", &schemas.RunContext{Experiment: &schemas.Experiment{Realism: schemas.RealismStandard}, Suite: &schemas.Suite{Realism: schemas.RealismNeutral}}, schemas.RealismStandard},
		{"experiment neutral", &schemas.RunContext{Experiment: &schemas.Experiment{Realism: schemas.RealismNeutral}}, schemas.RealismNeutral},
		{"unknown value stays standard", &schemas.RunContext{Experiment: &schemas.Experiment{Realism: "stealth"}}, schemas.RealismStandard},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := viewFor(tt.rc).realism; got != tt.want {
				t.Fatalf("realism %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNeutralEnvName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"BENCH_EVENTS_URL", "AGENT_EVENTS_URL"},
		{"BENCH_EVENTS_FILE", "AGENT_EVENTS_FILE"},
		{"BENCH_MCP_SERVERS", "AGENT_MCP_SERVERS"},
		{"BENCH_SESSION_ID", "AGENT_SESSION_ID"},
		{"BENCH_SESSION_INDEX", "AGENT_SESSION_INDEX"},
		{"BENCH_SESSION_COUNT", "AGENT_SESSION_COUNT"},
		{"BENCH_ANSWER_PATH", "AGENT_ANSWER_PATH"},
		{"BENCH_MEMORY_DIR", "AGENT_MEMORY_DIR"},
		{"BENCH_SKILLS_DIR", "AGENT_SKILLS_DIR"},
		{"BENCH_EXTERNAL_APIS", "AGENT_EXTERNAL_APIS"},
		{"BENCH_MODEL", "AGENT_MODEL"},
		{"BENCH_REMOTE_URL", "AGENT_REMOTE_URL"},
		{"BENCH_FAULT_URL", "AGENT_HOOK_URL"},
		{"BENCH_FAULT_HOOKS", "AGENT_HOOK_HOOKS"},
		{"BENCH_", "BENCH_"},
		{"OTEL_SERVICE_NAME", "OTEL_SERVICE_NAME"},
		{"HOME", "HOME"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NeutralEnvName(tt.in); got != tt.want {
				t.Fatalf("NeutralEnvName(%s) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestNeutralText(t *testing.T) {
	tests := []struct{ in, want string }{
		{`{"notes":{"url":"http://toolproxy:9200/s/notes/mcp"}}`, `{"notes":{"url":"http://tools.internal:9200/s/notes/mcp"}}`},
		{"cat /tmp/bench/instruction.md", "cat /tmp/task/instruction.md"},
		{"/tmp/benchmark/x stays", "/tmp/benchmark/x stays"},
		{`python3 -c 'import os; print(os.environ["BENCH_MCP_SERVERS"])'`, `python3 -c 'import os; print(os.environ["AGENT_MCP_SERVERS"])'`},
		{"model: {env:BENCH_MODEL}", "model: {env:AGENT_MODEL}"},
		{"${BENCH_FAULT_URL}", "${AGENT_HOOK_URL}"},
		{"/opt/bench/events/events.jsonl", "/opt/tools/events/events.jsonl"},
		{"/opt/bench/skills/pdf", "/opt/bench/skills/pdf"},
		{"MYBENCH_X and toolproxy alone", "MYBENCH_X and toolproxy alone"},
		{"plain text", "plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NeutralText(tt.in); got != tt.want {
				t.Fatalf("NeutralText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripCanaryLines(t *testing.T) {
	guid := "0f6c1a2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b"
	tests := []struct {
		name, in, want string
	}{
		{"markdown stamp", "Fix the bug.\n\n" + utils.CanaryLine("instruction.md", guid) + "\n", "Fix the bug.\n"},
		{"shell stamp crlf", "echo hi\r\n\r\n" + utils.CanaryLine("run.sh", guid) + "\r\n", "echo hi\r\n"},
		{"no trailing newline", "Fix.\n" + utils.CanaryLine("x.txt", guid), "Fix."},
		{"only the canary", utils.CanaryLine("x.md", guid) + "\n", ""},
		{"marker without notice is kept", "the bifrost-bench canary GUID " + guid + " is mentioned\n", "the bifrost-bench canary GUID " + guid + " is mentioned\n"},
		{"untouched", "Fix the bug.\n", "Fix the bug.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripCanaryLines(tt.in); got != tt.want {
				t.Fatalf("StripCanaryLines = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentPlan(t *testing.T) {
	guid := "0f6c1a2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b"
	instruction := "Fix the rounding bug.\n\n" + utils.CanaryLine("instruction.md", guid) + "\n"
	plan := func() *schemas.RunPlan {
		return &schemas.RunPlan{
			Image:   "img:1",
			Argv:    []string{"sh", "-c", `agent --mcp "$BENCH_MCP_SERVERS" --prompt-file /tmp/bench/instruction.md`, instruction},
			Env:     map[string]string{"BENCH_MCP_SERVERS": `{"notes":{"url":"http://toolproxy:9200/s/notes/mcp"}}`, "BENCH_MODEL": "m", "ANTHROPIC_BASE_URL": "http://bifrost:8080/anthropic"},
			Files:   map[string]string{"/tmp/bench/instruction.md": instruction, "/home/agent/.claude.json": `{"mcpServers":{"notes":{"url":"http://toolproxy:9200/s/notes/mcp"}}}`},
			Dirs:    map[string]string{"/home/agent/.claude/skills/pdf": "/host/skills/pdf"},
			Workdir: "/workspace",
		}
	}
	t.Run("standard returns the plan itself", func(t *testing.T) {
		p := plan()
		got, err := standardView.agentPlan(p)
		if err != nil || got != p {
			t.Fatalf("got %p err %v, want the same plan", got, err)
		}
	})
	t.Run("neutral rewrites a copy", func(t *testing.T) {
		p := plan()
		before := plan()
		got, err := neutralView.agentPlan(p)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p, before) {
			t.Fatal("agentPlan changed the bench's plan")
		}
		var all []string
		all = append(all, got.Argv...)
		for k, v := range got.Env {
			all = append(all, k, v)
		}
		for k, v := range got.Files {
			all = append(all, k, v)
		}
		for k := range got.Dirs {
			all = append(all, k)
		}
		for _, s := range all {
			if w := benchCue(s); w != "" {
				t.Fatalf("neutral plan shows %q in %q", w, s)
			}
		}
		if got.Env["AGENT_MCP_SERVERS"] != `{"notes":{"url":"http://tools.internal:9200/s/notes/mcp"}}` || got.Env["AGENT_MODEL"] != "m" || got.Env["ANTHROPIC_BASE_URL"] != "http://bifrost:8080/anthropic" {
			t.Fatalf("env %v", got.Env)
		}
		if got.Files["/tmp/task/instruction.md"] != "Fix the rounding bug.\n" || got.Argv[3] != "Fix the rounding bug.\n" {
			t.Fatalf("canary not stripped: %q / %q", got.Files["/tmp/task/instruction.md"], got.Argv[3])
		}
		if got.Argv[2] != `agent --mcp "$AGENT_MCP_SERVERS" --prompt-file /tmp/task/instruction.md` {
			t.Fatalf("argv %q", got.Argv[2])
		}
		if got.Dirs["/home/agent/.claude/skills/pdf"] != "/host/skills/pdf" || got.Image != "img:1" || got.Workdir != "/workspace" {
			t.Fatalf("plan %+v", got)
		}
	})
	t.Run("two names that become one are refused", func(t *testing.T) {
		p := plan()
		p.Env["AGENT_MODEL"] = "other"
		if _, err := neutralView.agentPlan(p); err == nil || !strings.Contains(err.Error(), "AGENT_MODEL") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("a path inside a neutral mount is refused", func(t *testing.T) {
		p := plan()
		p.Files["/opt/tools/x"] = "x"
		if _, err := neutralView.agentPlan(p); err == nil {
			t.Fatal("no error")
		}
	})
}

func TestBuildLaunchView(t *testing.T) {
	plan := &schemas.RunPlan{
		Argv:  []string{"agent", "go"},
		Env:   map[string]string{"HEADERS": "a\nb", "PLAIN": "p"},
		Files: map[string]string{"/tmp/task/instruction.md": "Fix it.\n"},
	}
	links := []link{{dst: "/home/agent/.claude/skills/pdf", src: neutralView.linkTarget("dirs", 0)}}
	std, err := buildLaunch(plan, "/opt/bench/shim:/usr/bin", "/home/agent", links)
	if err != nil {
		t.Fatal(err)
	}
	same, err := buildLaunchView(plan, "/opt/bench/shim:/usr/bin", "/home/agent", links, standardView)
	if err != nil || !reflect.DeepEqual(std, same) {
		t.Fatalf("standard view differs from buildLaunch: %v", err)
	}
	if std.Args[2] != "bench-wrap" || !strings.Contains(std.Args[1], "BENCH_WRAP_") {
		t.Fatalf("standard wrapper changed: %q", std.Args[:3])
	}
	l, err := buildLaunchView(plan, neutralView.shimDir()+":/usr/bin", "/home/agent", links, neutralView)
	if err != nil {
		t.Fatal(err)
	}
	if l.Entrypoint != wrapShell || l.Args[2] != "sh" {
		t.Fatalf("neutral wrapper $0 %q", l.Args[2])
	}
	for _, s := range append(append([]string{}, l.Env...), l.Args...) {
		if w := benchCue(s); w != "" {
			t.Fatalf("neutral launch shows %q in %q", w, s)
		}
	}
	if !strings.Contains(l.Args[1], "unset _ENTRY_") || !strings.Contains(strings.Join(l.Env, "\n"), "PATH=/opt/tools/shim:/usr/bin") {
		t.Fatalf("neutral launch:\n%s\n%v", l.Args[1], l.Env)
	}
	if _, err := buildLaunchView(&schemas.RunPlan{Argv: []string{"a"}, Env: map[string]string{"_ENTRY_X": "1"}}, "/usr/bin", "/home/agent", nil, neutralView); err == nil {
		t.Fatal("neutral wrapper prefix not reserved")
	}
}

func TestAgentCreateArgsView(t *testing.T) {
	h := &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"}
	plan := &schemas.RunPlan{Image: "img:1"}
	std := agentCreateArgs("bench-r_1", nil, plan, h, "evalnet", "/tmp/envf", &launch{}, nil, false)
	if got := agentCreateArgsView("bench-r_1", nil, plan, h, "evalnet", "/tmp/envf", &launch{}, nil, false, standardView); !reflect.DeepEqual(std, got) {
		t.Fatalf("standard view differs:\n%v\n%v", std, got)
	}
	neutral := strings.Join(agentCreateArgsView("bench-r_1", nil, plan, h, "evalnet", "/tmp/envf", &launch{}, nil, false, neutralView), " ")
	if !strings.Contains(neutral, "-v bench-aux-r_1:/opt/tools ") || strings.Contains(neutral, ":/opt/bench") {
		t.Fatalf("neutral args %s", neutral)
	}
}

func TestEventEnvView(t *testing.T) {
	rc := &schemas.RunContext{Agent: &schemas.AgentConfig{ID: "my-agent"}}
	on := newDockerWorkspace(DockerWorkspaceConfig{EventsCollector: true})
	collectorRuns.Store("r_view", true)
	defer collectorRuns.Delete("r_view")
	std := on.eventEnv(rc, "r_view", []string{"A=1"})
	if got := on.eventEnvView(rc, "r_view", []string{"A=1"}, standardView); !reflect.DeepEqual(std, got) {
		t.Fatalf("standard view differs: %v / %v", std, got)
	}
	got := on.eventEnvView(rc, "r_view", []string{"AGENT_EVENTS_FILE=/mine"}, neutralView)
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"AGENT_EVENTS_FILE=/mine",
		"AGENT_EVENTS_URL=http://tools.internal:9200/events",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://tools.internal:9200/otlp",
		"OTEL_RESOURCE_ATTRIBUTES=service.instance.id=r_view",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in\n%s", want, joined)
		}
	}
	if w := benchCue(joined); w != "" {
		t.Fatalf("neutral event env shows %q:\n%s", w, joined)
	}
	if strings.Count(joined, "AGENT_EVENTS_FILE=") != 1 {
		t.Fatalf("agent's own events file overridden:\n%s", joined)
	}
	notServing := strings.Join(on.eventEnvView(rc, "r_none", nil, neutralView), "\n")
	if notServing != "AGENT_EVENTS_FILE=/opt/tools/events/events.jsonl" {
		t.Fatalf("no collector: %s", notServing)
	}
}

func TestProxyAliasArgs(t *testing.T) {
	neutral := &schemas.RunContext{Experiment: &schemas.Experiment{Realism: schemas.RealismNeutral}}
	tests := []struct {
		name string
		rc   *schemas.RunContext
		want []string
	}{
		{"standard", &schemas.RunContext{}, []string{"--network-alias", "toolproxy", "--hostname", "toolproxy"}},
		{"neutral", neutral, []string{"--network-alias", "toolproxy", "--network-alias", "tools.internal", "--hostname", "tools.internal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := proxyAliasArgs(tt.rc); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcWatchCommand(t *testing.T) {
	tests := []struct {
		name string
		v    agentView
		want []string
	}{
		{"standard", standardView, []string{"--entrypoint", "python3", "helper:1", "-u", "-", "20"}},
		{"neutral", neutralView, []string{"-e", "POLL_MS=20", "--entrypoint", "python3", "helper:1", "-u", "-"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := procWatchCommand(tt.v, "helper:1", 20)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			// What the agent sees in ps: everything after the image.
			for i, a := range got {
				if a == "helper:1" {
					for _, s := range got[i+1:] {
						if w := benchCue(s); w != "" || strings.Contains(s, "watch") || strings.Contains(s, "observer") {
							t.Fatalf("observer argv shows %q", s)
						}
					}
				}
			}
		})
	}
}

func TestWriteRealism(t *testing.T) {
	plan := &schemas.RunPlan{Env: map[string]string{"BENCH_MODEL": "m", "HOME": "/h"}}
	t.Run("standard writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		if err := standardView.writeRealism(dir, plan, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, RealismFile)); !os.IsNotExist(err) {
			t.Fatalf("standard run wrote %s: %v", RealismFile, err)
		}
		if got, err := utils.ReadRealism(nil); err != nil || got != schemas.RealismStandard {
			t.Fatalf("utils.ReadRealism(nil) = %s, %v", got, err)
		}
	})
	t.Run("neutral records the renames", func(t *testing.T) {
		dir := t.TempDir()
		if err := neutralView.writeRealism(dir, plan, []string{"AGENT_EVENTS_FILE=/x", "HOME=/h"}); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, RealismFile))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := utils.ReadRealism(b); err != nil || got != schemas.RealismNeutral {
			t.Fatalf("ReadRealism = %s, %v", got, err)
		}
		for _, want := range []string{`"BENCH_MODEL": "AGENT_MODEL"`, `"BENCH_EVENTS_FILE": "AGENT_EVENTS_FILE"`, `"tool_host": "tools.internal"`, `"canary_hidden": true`} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("missing %s in %s", want, b)
			}
		}
	})
	t.Run("broken file is an error", func(t *testing.T) {
		if _, err := utils.ReadRealism([]byte("{")); err == nil {
			t.Fatal("no error")
		}
	})
}

func TestViewVolumeNames(t *testing.T) {
	tests := []struct {
		name string
		v    agentView
		want []string
	}{
		{"standard", standardView, []string{"bench-ws-r_1", "bench-aux-r_1", "bench-dir-r_1-0", "bench-mem-r_1", "bench-memp-r_1-0"}},
		{"neutral", neutralView, []string{"vol-ws-r_1", "vol-aux-r_1", "vol-dir-r_1-0", "vol-mem-r_1", "vol-memp-r_1-0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := append([]string{tt.v.workspaceVolume("r_1"), tt.v.auxVolume("r_1"), tt.v.dirVolume("r_1", 0)}, tt.v.memoryVolumes("r_1", 1)...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	if !reflect.DeepEqual(standardView.memoryVolumes("r_2", 2), memoryVolumes("r_2", 2)) {
		t.Fatal("standard memory volumes changed")
	}
}

func TestNeutralRunNames(t *testing.T) {
	neutral := &schemas.RunContext{Run: &schemas.Run{RunID: "r_names1"}, Experiment: &schemas.Experiment{Realism: schemas.RealismNeutral}}
	standard := &schemas.RunContext{Run: &schemas.Run{RunID: "r_names2"}}
	for _, rc := range []*schemas.RunContext{neutral, standard} {
		if _, _, err := runIDs(rc); err != nil {
			t.Fatal(err)
		}
	}
	defer neutralRuns.Delete("r_names1")
	tests := []struct {
		name, got, want string
	}{
		{"neutral agent", agentContainer("r_names1"), "ws-r_names1"},
		{"neutral network", serviceNetwork("r_names1"), "net-r_names1"},
		{"neutral session agent", agentName(&schemas.RunContext{Session: &schemas.SessionSpec{ID: "s"}, SessionIndex: 1}, "r_names1"), "ws-r_names1-s2"},
		{"neutral tool proxy keeps its name", toolProxyContainer("r_names1"), "bench-tp-r_names1"},
		{"standard agent", agentContainer("r_names2"), "bench-r_names2"},
		{"standard network", serviceNetwork("r_names2"), "bench-net-r_names2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("got %s, want %s", tt.got, tt.want)
			}
		})
	}
	if pickAgent("r_names1", []string{"ws-r_names1", "ws-r_names1-s2", "bench-r_names1"}) != "ws-r_names1-s2" {
		t.Fatal("snapshot cannot find a neutral run's agent container")
	}
}
