package environments

import (
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestComposeCreateArgs(t *testing.T) {
	spec := schemas.ServiceSpec{ID: "db", Argv: []string{"-c", "fsync=off"}, Compose: &schemas.ComposeService{
		Name: "db_main", Aliases: []string{"db_main", "database"}, User: "999", Workdir: "/srv",
		Tmpfs: []string{"/run:size=8m"}, Volumes: []schemas.ComposeVolume{{Name: "pgdata", Target: "/var/lib/postgresql"}, {Target: "/scratch"}, {Name: "conf", Target: "/etc/x", ReadOnly: true}},
		CPUs: 0.5, Entrypoint: []string{"docker-entrypoint.sh", "postgres"}, EntrypointSet: true,
	}}
	got := composeCreateArgs("r_1", spec)
	want := []string{"--network-alias", "db_main", "--network-alias", "database", "--user", "999", "--workdir", "/srv",
		"--tmpfs", "/run:size=8m", "-v", "bench-cv-r_1-pgdata:/var/lib/postgresql", "-v", "/scratch", "-v", "bench-cv-r_1-conf:/etc/x:ro",
		"--cpus", "0.5", "--entrypoint", "docker-entrypoint.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args\n got %q\nwant %q", got, want)
	}
	if argv := serviceArgv(spec); !reflect.DeepEqual(argv, []string{"postgres", "-c", "fsync=off"}) {
		t.Fatalf("argv %q", argv)
	}
	if composeCreateArgs("r_1", schemas.ServiceSpec{ID: "x"}) != nil || hiddenAliasArgs(schemas.ServiceSpec{}) != nil {
		t.Fatal("a plain service got compose flags")
	}
	clear := schemas.ServiceSpec{ID: "x", Compose: &schemas.ComposeService{EntrypointSet: true}}
	if a := composeCreateArgs("r", clear); !reflect.DeepEqual(a, []string{"--entrypoint", ""}) {
		t.Fatalf("cleared entrypoint %q", a)
	}
}

func TestServiceLimits(t *testing.T) {
	cases := []struct {
		x         *schemas.ComposeService
		pids, mem string
	}{
		{nil, "1024", "4g"},
		{&schemas.ComposeService{Pids: 64, MemoryBytes: 256 << 20}, "64", "268435456"},
		{&schemas.ComposeService{Pids: 5000, MemoryBytes: 8 << 30}, "1024", "4g"},
	}
	for _, c := range cases {
		p, m := serviceLimits(schemas.ServiceSpec{Compose: c.x})
		if p != c.pids || m != c.mem {
			t.Errorf("%+v: %s %s", c.x, p, m)
		}
	}
}

func TestWithTaskEnv(t *testing.T) {
	plan := &schemas.RunPlan{Image: "i", Env: map[string]string{"A": "plan", "BENCH_X": "1"}}
	rc := &schemas.RunContext{Task: &schemas.Task{Environment: &schemas.TaskEnvironment{Env: map[string]string{"A": "task", "DATABASE_URL": "postgres://db"}}}}
	got := withTaskEnv(rc, plan)
	if got == plan || !reflect.DeepEqual(got.Env, map[string]string{"A": "plan", "BENCH_X": "1", "DATABASE_URL": "postgres://db"}) {
		t.Fatalf("env %v", got.Env)
	}
	if len(plan.Env) != 2 {
		t.Fatal("the plan itself was changed")
	}
	if withTaskEnv(&schemas.RunContext{Task: &schemas.Task{}}, plan) != plan {
		t.Fatal("a task without env copied the plan")
	}
}

// TestDiffRemovedCommentLine: a removed line that starts with "-- " (an SQL
// comment, as in a migration) reads "--- ..." inside the hunk and an added
// "++ x" reads "+++ x"; neither is a file marker, so the patch parses with
// its real path (found by a compose task whose mutant rewrote a migration).
func TestDiffRemovedCommentLine(t *testing.T) {
	patch := "diff --git a/migrations/002.sql b/migrations/002.sql\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/migrations/002.sql\n" +
		"+++ b/migrations/002.sql\n" +
		"@@ -1,2 +1,2 @@\n" +
		"--- 002: every product gets a sku\n" +
		"-ALTER TABLE products ADD COLUMN sku TEXT NOT NULL;\n" +
		"+++ counter\n" +
		"+ALTER TABLE products ADD COLUMN sku TEXT;\n"
	f, err := filterDiff([]byte(patch))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Files) != 1 || f.Files[0].Path != "migrations/002.sql" || string(f.Kept) != patch {
		t.Fatalf("files %+v", f.Files)
	}
}
