package deploy

// Lane H08 env sweep: every HEIMDALL_, BENCH_, BIFROST_ or LIVEKIT_ variable
// the bench code reads is either an operator setting (in
// hosted/.env.example, with a row in docs/hosting.md), fixed by the hosted
// compose file or server image, or deliberately local only (localOnlyEnv:
// the CLI, CI, tests and per-run container environment).

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// localOnlyEnv are variables the code reads that a hosted operator never
// sets, with why.
var localOnlyEnv = map[string]string{
	"BENCH_ADMISSIONS_ROOT":               "local store override, the hosted config sets the root",
	"BENCH_MONITOR_CERTS_ROOT":            "local store override, the hosted config sets the root",
	"BENCH_WRITE_TOKEN":                   "local server write token; hosted mode uses accounts",
	"BENCH_WORKER_TOKEN":                  "an organization's own worker (docs/hosting.md section 6), not the server",
	"BENCH_MODEL":                         "local gateway defaults for `bifrost-bench` on a laptop",
	"BENCH_OPENAI_MODEL":                  "local gateway defaults for `bifrost-bench` on a laptop",
	"BENCH_SIM_MODE":                      "set by the bench inside simulated-user containers",
	"BENCH_GATE_TOKEN_SHA256":             "set by the bench inside a run's tool gate container",
	"BENCH_FAULTPROXY_PROVIDERS":          "fault proxy flags, fixed in the compose files",
	"BENCH_FAULTPROXY_UPSTREAM":           "fault proxy flags, fixed in the compose files",
	"BENCH_FAULTPROXY_SLOTS":              "fault proxy flags, fixed in the compose files",
	"BENCH_FAULTPROXY_OPENAI_SLOTS":       "fault proxy flags, fixed in the compose files",
	"BENCH_FAULTPROXY_SCRIPTED_FALLBACK":  "fault proxy flags, fixed in the compose files",
	"HEIMDALL_URL":                        "CLI: the server `heimdall login` talks to",
	"HEIMDALL_TOKEN":                      "CLI and CI: the caller's API token",
	"HEIMDALL_SERVER":                     "CLI project sync",
	"HEIMDALL_CONFIG_DIR":                 "CLI credentials folder",
	"HEIMDALL_BENCHMARKS":                 "CI action input",
	"HEIMDALL_FAULTS":                     "CI action input",
	"HEIMDALL_CATALOG_ROOT":               "local benchmark catalog override",
	"HEIMDALL_DATASETS_DIR":               "local benchmark catalog import folder",
	"HEIMDALL_DEV":                        "developer flag",
	"HEIMDALL_ADMIT_REFERENCE_RUNS":       "local admission override",
	"HEIMDALL_EGRESS_MODE":                "set by the bench per run in the egress container",
	"HEIMDALL_EGRESS_ALLOW":               "set by the bench per run in the egress container",
	"HEIMDALL_STRIPE_API_BASE":            "tests: a fake Stripe",
	"HEIMDALL_TEST_DB":                    "tests",
	"HEIMDALL_TEST_PG_DSN":                "tests",
	"HEIMDALL_TEST_MINIO_IMAGE":           "tests",
	"HEIMDALL_RUNNER_REPO":                "`runner install` script default on an organization's own worker",
	"HEIMDALL_SHARED_BUILDER":             "shared worker flag default; compose passes the buildkit address",
	"HEIMDALL_SHARED_BUILDKIT":            "shared worker flag default; compose passes the buildkit address",
	"HEIMDALL_LIBRARY_REGISTRY_ISSUER":    "library-registry.compose.yml sets it",
	"HEIMDALL_LIBRARY_REGISTRY_SERVICE":   "library-registry.compose.yml sets it",
	"HEIMDALL_LIBRARY_REGISTRY_SYNC_AUTH": "library-registry.compose.yml sets it",
	"HEIMDALL_DOWNLOADS_DIR":              "fixed in the server image (Dockerfile.server)",
}

var (
	envNameRe   = regexp.MustCompile(`^(HEIMDALL|BENCH|BIFROST|LIVEKIT)_[A-Z0-9_]+$`)
	readFn      = `(?:Getenv|LookupEnv|envOr\w*|envInt|envBool|envDur\w*|getenv\w*)\(\s*`
	readLitRe   = regexp.MustCompile(readFn + `"([A-Z0-9_]+)"`)
	readIdentRe = regexp.MustCompile(readFn + `(?:\w+\.)?(\w+)\s*[,)]`)
	envConstRe  = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*(?:string\s*)?=\s*"((?:HEIMDALL|BENCH|BIFROST|LIVEKIT)_[A-Z0-9_]+)"`)
)

// benchEnvReads returns the prefixed variables the bench's non-test Go
// code reads, with a file that reads each.
func benchEnvReads(t *testing.T) map[string]string {
	t.Helper()
	srcs := map[string]string{}
	err := filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "ui", "node_modules", "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		srcs[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, s := range srcs {
		all.WriteString(s)
		all.WriteByte('\n')
	}
	out := map[string]string{}
	for p, s := range srcs {
		for _, m := range readLitRe.FindAllStringSubmatch(s, -1) {
			if envNameRe.MatchString(m[1]) {
				out[m[1]] = p
			}
		}
	}
	consts := map[string][][2]string{}
	for p, s := range srcs {
		for _, m := range envConstRe.FindAllStringSubmatch(s, -1) {
			consts[m[1]] = append(consts[m[1]], [2]string{m[2], p})
		}
	}
	readIdents := map[string]bool{}
	for _, m := range readIdentRe.FindAllStringSubmatch(all.String(), -1) {
		readIdents[m[1]] = true
	}
	for ident, vals := range consts {
		if readIdents[ident] {
			for _, v := range vals {
				out[v[0]] = v[1]
			}
		}
	}
	return out
}

func TestEnvSweep(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	example := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^#? ?([A-Z][A-Z0-9_]+)=`).FindAllStringSubmatch(read("hosted/.env.example"), -1) {
		example[m[1]] = true
	}
	fixed := read("hosted/docker-compose.yml") + read("hosted/library-registry.compose.yml") + read("hosted/Dockerfile.server")
	hosting := read("../docs/hosting.md")

	var missing []string
	for name, file := range benchEnvReads(t) {
		if example[name] || localOnlyEnv[name] != "" || regexp.MustCompile(`\b`+name+`\b`).MatchString(fixed) {
			continue
		}
		missing = append(missing, name+" ("+file+")")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("read by the bench but not in deploy/hosted/.env.example, the hosted compose files or localOnlyEnv:\n  %s", strings.Join(missing, "\n  "))
	}
	var noRow []string
	for name := range example {
		if envNameRe.MatchString(name) && !regexp.MustCompile("`"+name+"[`=]").MatchString(hosting) {
			noRow = append(noRow, name)
		}
	}
	sort.Strings(noRow)
	if len(noRow) > 0 {
		t.Errorf("in deploy/hosted/.env.example without a docs/hosting.md row: %s", strings.Join(noRow, ", "))
	}
}
