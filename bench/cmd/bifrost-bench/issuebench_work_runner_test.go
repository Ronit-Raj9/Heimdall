package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/work"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const ibwFakeToken = "ghp_FAKEtoken0123456789abcdefABCDEF"

func ibwRequest(org string) issuebench.WorkRequest {
	c := issuebench.Candidate{Repo: "acme/widgets", BaseSHA: strings.Repeat("a", 40), TestPatch: "diff test", FixPatch: "diff fix"}
	return issuebench.WorkRequest{Mode: issuebench.WorkModeValidate, OrgID: org, JobID: "job-1", Repo: "acme/widgets",
		Version: "2026-11", Candidate: c, Env: &issuebench.EnvSpec{Dockerfile: "FROM python:3.12\n"}}
}

func ibwLease(t *testing.T, req issuebench.WorkRequest) *schemas.JobLease {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(issuebench.WorkRequestFile)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(raw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return &schemas.JobLease{JobID: req.JobID, JobKind: issuebench.WorkJobKind, Argv: issuebench.WorkArgv(), Files: buf.Bytes(),
		Secrets: map[string]string{issuebench.SecretGitHubToken: ibwFakeToken, issuebench.SecretGatewayKey: "sk-bf-fakevirtualkey-123456"}}
}

func ibwExecutor(org string, run func(context.Context, work.Deps, issuebench.WorkRequest, map[string]string) (issuebench.WorkResult, error)) *ibWorkExecutor {
	return &ibWorkExecutor{org: org, sem: make(chan struct{}, 1), newDeps: func(dir string) work.Deps { return work.Deps{Scratch: dir} }, runWork: run}
}

func ibwOK(context.Context, work.Deps, issuebench.WorkRequest, map[string]string) (issuebench.WorkResult, error) {
	return issuebench.WorkResult{Mode: issuebench.WorkModeValidate, Validation: &issuebench.Validation{FailToPass: []string{"t1"}}}, nil
}

func TestIssuebenchWorkRefusesWrongKindAndArgv(t *testing.T) {
	x := ibwExecutor("", ibwOK)
	base := ibwLease(t, ibwRequest("org-a"))
	kind := *base
	kind.JobKind = "agent.build"
	argv := *base
	argv.Argv = []string{"issuebench", "work", "-request", "../../etc/passwd", "-result", issuebench.WorkResultFile}
	for name, l := range map[string]*schemas.JobLease{"nil": nil, "kind": &kind, "argv": &argv} {
		run, err := x.RunJob(context.Background(), l, func(string) {})
		if err == nil || !strings.Contains(err.Error(), "refuses") || run != nil {
			t.Fatalf("%s: want a refusal before any folder, got run %v err %v", name, run, err)
		}
	}
	// The plain runner executor hands issuebench.work to this executor first.
	_, err := runnerJobExecutor{}.RunJob(context.Background(), &argv, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "issuebench.work") {
		t.Fatalf("runnerJobExecutor did not dispatch the kind: %v", err)
	}
	if !runnerJobCommands[issuebench.WorkCommand] {
		t.Fatal("issuebench is not a runner job command")
	}
}

func TestIssuebenchWorkRefusesAnotherOrg(t *testing.T) {
	called := false
	x := ibwExecutor("org-a", func(ctx context.Context, d work.Deps, r issuebench.WorkRequest, s map[string]string) (issuebench.WorkResult, error) {
		called = true
		return ibwOK(ctx, d, r, s)
	})
	run, err := x.RunJob(context.Background(), ibwLease(t, ibwRequest("org-b")), func(string) {})
	if err == nil || err.Error() != "this job belongs to another organization" || called {
		t.Fatalf("got err %v called %v", err, called)
	}
	if run != nil && run.Cleanup != nil {
		run.Cleanup()
	}
	// The same org runs.
	run, err = x.RunJob(context.Background(), ibwLease(t, ibwRequest("org-a")), func(string) {})
	if err != nil || !called {
		t.Fatalf("own org: err %v called %v", err, called)
	}
	run.Cleanup()
}

func TestIssuebenchWorkRefusesBadRequest(t *testing.T) {
	x := ibwExecutor("", ibwOK)
	req := ibwRequest("org-a")
	req.Candidate.BaseSHA = "main"
	run, err := x.RunJob(context.Background(), ibwLease(t, req), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "full commit id") {
		t.Fatalf("got %v", err)
	}
	run.Cleanup()
}

func TestIssuebenchWorkRedactsSecrets(t *testing.T) {
	var lines []string
	var mu sync.Mutex
	x := ibwExecutor("", func(ctx context.Context, d work.Deps, r issuebench.WorkRequest, s map[string]string) (issuebench.WorkResult, error) {
		tok := s[issuebench.SecretGitHubToken]
		if tok != ibwFakeToken {
			t.Errorf("the work core did not get the lease secrets")
		}
		d.Log("fetching with token %s\nheader %s", tok, "Authorization: Basic "+b64("x-access-token:"+tok))
		d.Log("model key %s", s[issuebench.SecretGatewayKey])
		return issuebench.WorkResult{Mode: r.Mode, Stop: "stopped: " + tok}, errors.New("git failed for " + tok)
	})
	run, err := x.RunJob(context.Background(), ibwLease(t, ibwRequest("org-a")), func(l string) {
		mu.Lock()
		lines = append(lines, l)
		mu.Unlock()
	})
	defer run.Cleanup()
	all := strings.Join(lines, "\n") + "\n" + err.Error() + "\n" + string(run.Result)
	for _, v := range []string{ibwFakeToken, "sk-bf-fakevirtualkey-123456", b64("x-access-token:" + ibwFakeToken)} {
		if strings.Contains(all, v) {
			t.Fatalf("secret %q leaked:\n%s", v, all)
		}
	}
	if len(lines) != 3 || !strings.Contains(all, "[redacted]") {
		t.Fatalf("want 3 redacted lines, got %q", lines)
	}
	var res issuebench.WorkResult
	if err := json.Unmarshal(run.Result, &res); err != nil {
		t.Fatalf("redacted result is not JSON: %v", err)
	}
}

func TestIssuebenchWorkResultCapAndCleanup(t *testing.T) {
	var scratch string
	x := ibwExecutor("", func(_ context.Context, d work.Deps, r issuebench.WorkRequest, _ map[string]string) (issuebench.WorkResult, error) {
		scratch = d.Scratch
		return issuebench.WorkResult{Mode: r.Mode, Stop: strings.Repeat("x", issuebench.MaxWorkResultBytes)}, nil
	})
	run, err := x.RunJob(context.Background(), ibwLease(t, ibwRequest("org-a")), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "byte cap") || run.Result != nil {
		t.Fatalf("want the cap error and no result, got %v", err)
	}
	if run.Dir == "" || scratch != run.Dir {
		t.Fatalf("scratch %q is not the job dir %q", scratch, run.Dir)
	}
	if _, err := os.Stat(run.Dir); err != nil {
		t.Fatalf("job dir missing before cleanup: %v", err)
	}
	run.Cleanup()
	if _, err := os.Stat(run.Dir); !os.IsNotExist(err) {
		t.Fatalf("cleanup left %s: %v", run.Dir, err)
	}
}

func TestIssuebenchWorkSemaphoreSerializes(t *testing.T) {
	var active, peak atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	x := ibwExecutor("", func(ctx context.Context, d work.Deps, r issuebench.WorkRequest, s map[string]string) (issuebench.WorkResult, error) {
		n := active.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return ibwOK(ctx, d, r, s)
	})
	var waited atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := x.RunJob(context.Background(), ibwLease(t, ibwRequest("org-a")), func(l string) {
				if l == "waiting for a free build slot on this worker" {
					waited.Store(true)
				}
			})
			if err != nil {
				t.Errorf("job: %v", err)
			}
			run.Cleanup()
		}()
	}
	<-started
	deadline := time.After(5 * time.Second)
	for !waited.Load() {
		select {
		case <-deadline:
			t.Fatal("the second job did not wait for the slot")
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(release)
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatalf("%d jobs ran at once, want 1", peak.Load())
	}
}

func TestIssuebenchWorkWaitEndsWithContext(t *testing.T) {
	x := ibwExecutor("", ibwOK)
	x.sem <- struct{}{} // the slot is taken
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	run, err := x.RunJob(ctx, ibwLease(t, ibwRequest("org-a")), func(string) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	run.Cleanup()
}

func TestIssuebenchWorkSharedExecutorDefaults(t *testing.T) {
	t.Setenv(buildNetworkEnv, "")
	x := newSharedIssuebenchExecutor(&sharedHost{runtime: "runsc", prefix: "shared-h"}, nil, "org-a", ibwDocker())
	if x.org != "org-a" || x.publicGW || x.egress.Public != defaultBuildNetwork || x.egress.Runtime != "runsc" || x.egress.Runner != "shared-h" || x.egress.Org != "org-a" {
		t.Fatalf("shared executor: %+v", x)
	}
	if cap(x.sem) < 1 || x.sem != newSharedIssuebenchExecutor(nil, nil, "org-b", ibwDocker()).sem {
		t.Fatal("org loops must share one build slot pool")
	}
	own := newOwnIssuebenchExecutor(nil)
	if own.org != "" || !own.publicGW || own.egress.Public != "bridge" || cap(own.sem) != 1 {
		t.Fatalf("own executor: %+v", own)
	}
}

func TestIssuebenchWorkStdinSecrets(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "s")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"github_token":"` + ibwFakeToken + `"}`)
	f.Seek(0, 0)
	sec, err := readStdinSecrets(f)
	if err != nil || sec[issuebench.SecretGitHubToken] != ibwFakeToken {
		t.Fatalf("got %v %v", sec, err)
	}
	f.Truncate(0)
	f.Seek(0, 0)
	f.WriteString("not json " + ibwFakeToken)
	f.Seek(0, 0)
	if _, err := readStdinSecrets(f); err == nil || strings.Contains(err.Error(), ibwFakeToken) {
		t.Fatalf("bad stdin: %v", err)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func ibwDocker() utils.Docker { return utils.Docker{} }
