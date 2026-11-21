package skillpool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestImport(t *testing.T) {
	tmp := t.TempDir()
	base := filepath.Join(tmp, "base")
	write(t, base, map[string]string{
		"suite.yaml":                 "id: hd-harbor-skills\nversion: 1.0.0\ntasks:\n  - t1\n  - t2\n",
		"suite.lock":                 "not copied",
		"experiment-skills.yaml":     "not copied",
		"base/Dockerfile":            "FROM x",
		"tasks/t1/task.yaml":         "id: t1\nskills: skills\n",
		"tasks/t1/skills/a/SKILL.md": "a",
		"tasks/t2/task.yaml":         "id: t2\n",
	})
	from := filepath.Join(tmp, "ds")
	write(t, from, map[string]string{
		"repo/data/find-skills/SKILL.md":                                  "---\nname: finding-skills\n---\ncurl -s \"http://localhost:8742/keyword?q=x\"\n",
		"repo/search_server/http_server.py":                               "x",
		"repo/search_server/bm25_search.py":                               "x",
		"repo/search_server/semantic_search.py":                           "x",
		"hf/unz/skills.db":                                                "db",
		"hf/unz/o--n/SKILL.md":                                            "pool skill",
		"hf/skillsbench/curated/t1/skills/a/SKILL.md":                     "a",
		"hf/skillsbench/curated/t1/skills/x Y (z)/SKILL.md":               "s",
		"hf/skillsbench/curated/t1/skills/partial/scripts/a.py":           "no SKILL.md",
		"hf/skillsbench/retrieved_w_curated/claude/t1/skills/r/SKILL.md":  "r",
		"hf/skillsbench/retrieved_wo_curated/claude/t2/skills/q/SKILL.md": "q",
	})
	im := &importer{}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: tmp, From: from, SuiteID: "hd-harbor-skills-pool",
		Extra: map[string]string{"base": base, "benchmark": "skillsbench"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 2 {
		t.Fatalf("tasks %v", res.Tasks)
	}
	sd := res.SuiteDir
	for _, p := range []string{"base/Dockerfile", "tasks/t1/skills/a/SKILL.md", "tasks/t1/skill-sets/curated/a/SKILL.md", "tasks/t1/skill-sets/curated/x-Y--z-/SKILL.md",
		"tasks/t1/skill-sets/retrieved/r/SKILL.md", "tasks/t2/skill-sets/retrieved-wo-curated/q/SKILL.md",
		"tasks/t2/skill-sets/pool/finding-skills/SKILL.md", "services/skills-pool/Dockerfile",
		"services/skills-pool/search_server/index/skills.db", "services/skills-pool/skills/o--n/SKILL.md", "experiment-skills-pool.yaml"} {
		if _, err := os.Stat(filepath.Join(sd, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	for _, p := range []string{"suite.lock", "experiment-skills.yaml"} {
		if _, err := os.Stat(filepath.Join(sd, p)); err == nil {
			t.Errorf("%s copied", p)
		}
	}
	b, _ := os.ReadFile(filepath.Join(sd, "tasks/t2/skill-sets/pool/finding-skills/SKILL.md"))
	if !strings.Contains(string(b), "http://skills-pool:8742/keyword") || strings.Contains(string(b), "localhost") {
		t.Errorf("pool skill not rewritten: %s", b)
	}
	y, _ := os.ReadFile(filepath.Join(sd, "suite.yaml"))
	for _, want := range []string{"id: hd-harbor-skills-pool", "skills-pool", "bench/skills-pool:1"} {
		if !strings.Contains(string(y), want) {
			t.Errorf("suite.yaml lacks %q:\n%s", want, y)
		}
	}
	if _, err := im.Import(context.Background(), importers.Options{BenchRoot: tmp, From: from, SuiteID: "x", Extra: map[string]string{"base": base, "benchmark": "nope"}}); err == nil {
		t.Error("bad benchmark accepted")
	}
}
