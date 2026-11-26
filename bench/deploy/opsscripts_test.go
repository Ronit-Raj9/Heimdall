package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The operations scripts in hosted/ops (docs/operations.md). They are
// never run against a server here: the cleanup runs on a fake data folder
// with HEIMDALL_OPS_EXEC=direct, the escrow on a fake env file.

var opsScripts = []string{
	"offsite-backup.sh",
	"restore-drill.sh",
	"escrow-secrets.sh",
	"cleanup-pass6.sh",
	"uptime-probe.sh",
}

func opsPath(name string) string { return filepath.Join("hosted", "ops", name) }

func runOps(t *testing.T, env []string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{opsPath(name)}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = nil // not a terminal
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestOpsScriptsSyntaxAndHelp(t *testing.T) {
	for _, s := range opsScripts {
		if out, err := exec.Command("bash", "-n", opsPath(s)).CombinedOutput(); err != nil {
			t.Errorf("%s: bash -n: %v\n%s", s, err, out)
			continue
		}
		out, err := runOps(t, nil, s, "-h")
		if err != nil {
			t.Errorf("%s -h: %v\n%s", s, err, out)
			continue
		}
		if !strings.Contains(out, "deploy/hosted/ops/"+s) {
			t.Errorf("%s -h does not print its header:\n%s", s, out)
		}
	}
	for _, u := range []string{"heimdall-offsite-backup.service", "heimdall-offsite-backup.timer", "heimdall-uptime.service", "heimdall-uptime.timer"} {
		if _, err := os.Stat(opsPath(u)); err != nil {
			t.Errorf("missing unit %s: %v", u, err)
		}
	}
}

func plant(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpsCleanupDryRunListsExactlyThePlantedLeftovers(t *testing.T) {
	root := t.TempDir()
	a := "runs/projects/org_a/"
	b := "orgs/org_b/"
	plant(t, root, map[string]string{
		a + "agents/zz-test-p01-agent.yaml":                        "x",
		a + "agents/zz-test-p01-agent/v1/agent.yaml":               "x",
		a + "agents/zz-test-p02-con2.yaml":                         "x",
		a + "suites/zz-test-p10-csv/suite.yaml":                    "x",
		a + "suites/zz-test-p11-qa/suite.yaml":                     "x",
		a + "experiments/e_zz_test_p05_cmp_fxv7/experiment.yaml":   "x",
		a + "experiments/e_canary_zz_test_p13_drift1_y8zi/x.json":  "x",
		a + "uploads/datasets/import-msibench/zz-test-p09-bad.csv": "x",
		a + "policies/monitors/zz-test-p13-keyword.yaml":           "x",
		a + "livescore/config.yaml":                                "keys: [zz-test-p13-vk-current]",
		a + "inbox/items/inc-2dcade20a283/proposal.json":           "x",
		a + "inbox/index.jsonl":                                    "inc-2dcade20a283\nreal-item\n",
		a + "drafts/ag_28d214e45e37.json":                          "x",
		b + "suites/zz-test-p03-own/suite.yaml":                    "x",
		// Not leftovers: must not be listed.
		a + "agents/zz-test-loop.yaml":                             "x",
		a + "agents/zz-test-p01-agent-real.yaml":                   "x",
		a + "suites/zz-test-p11-qb/suite.yaml":                     "x",
		a + "experiments/e_zz_test_p05_other_aaaa/experiment.yaml": "x",
		b + "livescore/config.yaml":                                "keys: [real]",
		b + "uploads/keep.csv":                                     "x",
	})
	want := []string{
		a + "agents/zz-test-p01-agent",
		a + "agents/zz-test-p01-agent.yaml",
		a + "agents/zz-test-p02-con2.yaml",
		a + "suites/zz-test-p10-csv",
		a + "suites/zz-test-p11-qa",
		a + "experiments/e_zz_test_p05_cmp_fxv7",
		a + "experiments/e_canary_zz_test_p13_drift1_y8zi",
		a + "uploads/datasets/import-msibench/zz-test-p09-bad.csv",
		a + "policies/monitors/zz-test-p13-keyword.yaml",
		a + "livescore/config.yaml",
		a + "inbox/items/inc-2dcade20a283",
		a + "drafts/ag_28d214e45e37.json",
		b + "suites/zz-test-p03-own",
	}
	before := snapshot(t, root)
	out, err := runOps(t, []string{"HEIMDALL_OPS_ROOT=" + root, "HEIMDALL_OPS_EXEC=direct"}, "cleanup-pass6.sh")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	moveSection := strings.SplitN(out, "Files that mention", 2)[0]
	moveSection = strings.SplitN(moveSection, "Database rows", 2)[0]
	var got []string
	for _, line := range strings.Split(moveSection, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, root+"/") {
			got = append(got, strings.TrimPrefix(line, root+"/"))
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("listed paths:\n%s\nwant:\n%s\nfull output:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), out)
	}
	if !strings.Contains(out, a+"inbox/index.jsonl") || !strings.Contains(out, "not moved") {
		t.Errorf("a shared inbox file that mentions a test item should be listed as not moved:\n%s", out)
	}
	if !strings.Contains(out, "DELETE FROM api_tokens WHERE name LIKE 'zz-test-%' AND revoked_at IS NOT NULL;") {
		t.Errorf("dry run does not show the token SQL:\n%s", out)
	}
	if !strings.Contains(out, "Dry run: nothing was moved or deleted.") {
		t.Errorf("dry run does not say it changed nothing:\n%s", out)
	}
	if after := snapshot(t, root); after != before {
		t.Errorf("the dry run changed the data folder")
	}
}

func TestOpsCleanupApplyRefusesWithoutTheFlags(t *testing.T) {
	root := t.TempDir()
	plant(t, root, map[string]string{"runs/projects/org_a/agents/zz-test-p01-agent.yaml": "x"})
	env := []string{"HEIMDALL_OPS_ROOT=" + root, "HEIMDALL_OPS_EXEC=direct", "HEIMDALL_BACKUP_DIR=" + t.TempDir()}
	before := snapshot(t, root)
	cases := map[string][]string{
		"--apply needs --i-reviewed-the-list":   {"--apply"},
		"--apply needs an interactive terminal": {"--apply", "--i-reviewed-the-list"},
	}
	for text, args := range cases {
		out, err := runOps(t, env, "cleanup-pass6.sh", args...)
		if err == nil {
			t.Errorf("%v: should refuse\n%s", args, out)
		}
		if !strings.Contains(out, text) {
			t.Errorf("%v: want %q in:\n%s", args, text, out)
		}
	}
	if after := snapshot(t, root); after != before {
		t.Errorf("a refused apply changed the data folder")
	}
}

func TestOpsEscrowNeverPrintsASecret(t *testing.T) {
	if _, err := exec.LookPath("age"); err != nil {
		t.Skip("age not installed")
	}
	if _, err := exec.LookPath("age-keygen"); err != nil {
		t.Skip("age-keygen not installed")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "escrow.key")
	if out, err := exec.Command("age-keygen", "-o", key).CombinedOutput(); err != nil {
		t.Fatalf("age-keygen: %v\n%s", err, out)
	}
	pubOut, err := exec.Command("age-keygen", "-y", key).Output()
	if err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(dir, "escrow.pub")
	if err := os.WriteFile(pub, pubOut, 0o644); err != nil {
		t.Fatal(err)
	}
	const secret = "planted-secret-value-7f3a"
	envFile := filepath.Join(dir, "hosted.env")
	if err := os.WriteFile(envFile, []byte("HEIMDALL_MASTER_KEY="+secret+"\nHEIMDALL_PG_PASSWORD=pg-"+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	env := []string{"HEIMDALL_ENV_FILE=" + envFile, "HEIMDALL_ESCROW_PUB=" + pub}
	got, err := runOps(t, env, "escrow-secrets.sh", "--dir", out)
	if err != nil {
		t.Fatalf("escrow: %v\n%s", err, got)
	}
	if strings.Contains(got, secret) {
		t.Fatalf("escrow output contains the secret:\n%s", got)
	}
	if !strings.Contains(got, "with 2 values; sha256 of the file:") {
		t.Errorf("unexpected output:\n%s", got)
	}
	files, _ := filepath.Glob(filepath.Join(out, "heimdall-secrets-*.age"))
	if len(files) != 1 {
		t.Fatalf("want one escrow file, got %v", files)
	}
	if b, _ := os.ReadFile(files[0]); strings.Contains(string(b), secret) {
		t.Fatal("escrow file holds the secret in plain text")
	}
	got, err = runOps(t, env, "escrow-secrets.sh", "--verify", files[0], "--identity", key)
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, got)
	}
	if strings.Contains(got, secret) || !strings.Contains(got, "HEIMDALL_MASTER_KEY matches") {
		t.Errorf("verify output:\n%s", got)
	}
}

// snapshot lists every path under root, for "nothing moved" checks.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	_ = filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	return strings.Join(paths, "\n")
}
