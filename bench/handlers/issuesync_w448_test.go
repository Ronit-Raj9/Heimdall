package handlers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/issuesync"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/valyala/fasthttp"
)

func issueSyncServe(t *testing.T, env OrgEnv, method, uri, body string) (int, []byte) {
	t.Helper()
	r := router.New()
	registerIssueSync(env, r)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	ctx.Request.SetBodyString(body)
	r.Handler(&ctx)
	return ctx.Response.StatusCode(), append([]byte(nil), ctx.Response.Body()...)
}

func issueSyncEnv(t *testing.T) OrgEnv {
	t.Helper()
	return OrgEnv{Mode: "local", Cfg: &lib.Config{Root: t.TempDir()}}
}

func TestScheduleWrittenAndRemoved(t *testing.T) {
	env := issueSyncEnv(t)
	code, body := issueSyncServe(t, env, "POST", APIPrefix+"/issuesync", `{"repo":"a/b","weekday":0,"hour_utc":5}`)
	if code != 200 {
		t.Fatalf("create %d %s", code, body)
	}
	var r issuesync.Repo
	_ = json.Unmarshal(body, &r)
	if r.Benchmark != "issues-a-b" || r.Weekday != 0 || r.MaxIssues != issuesync.DefaultMaxIssues || r.WeeklyUSD != issuesync.DefaultWeeklyUSD {
		t.Fatalf("repo %+v", r)
	}
	f, err := schedule.Load(WebSchedulesPath(env.Cfg.Root))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := f.Find(schedule.IssueSyncPrefix + r.ID)
	if !ok || s.Job != schedule.JobIssueSync || s.Cron != "0 5 * * 0" || s.Timezone != "UTC" {
		t.Fatalf("schedules %+v", f.Schedules)
	}
	if err := f.Validate(env.Cfg.Root, nil); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if code, body := issueSyncServe(t, env, "POST", APIPrefix+"/issuesync", `{"repo":"a/b","max_issues":31}`); code != 422 || !strings.Contains(string(body), "from 1 to 30") {
		t.Fatalf("bad field %d %s", code, body)
	}
	if code, _ := issueSyncServe(t, env, "DELETE", APIPrefix+"/issuesync/"+r.ID, ""); code != 204 {
		t.Fatalf("delete %d", code)
	}
	f, _ = schedule.Load(WebSchedulesPath(env.Cfg.Root))
	if _, ok := f.Find(schedule.IssueSyncPrefix + r.ID); ok {
		t.Fatal("schedule kept after delete")
	}
}

func TestSyncJobParamsHaveNoToken(t *testing.T) {
	env := issueSyncEnv(t)
	t.Setenv("GITHUB_TOKEN", "ghp_secretsecretsecret")
	code, body := issueSyncServe(t, env, "POST", APIPrefix+"/issuesync", `{"repo":"a/b"}`)
	if code != 200 {
		t.Fatalf("create %d %s", code, body)
	}
	var r issuesync.Repo
	_ = json.Unmarshal(body, &r)
	old := issueSyncPublic
	issueSyncPublic = func(context.Context, string) bool { return false }
	defer func() { issueSyncPublic = old }()
	// No scanner on this server: the run fails after the private check, and
	// neither the record, the schedule nor the answer may hold the token.
	code, runBody := issueSyncServe(t, env, "POST", APIPrefix+"/issuesync/"+r.ID+"/run", `{"yes":true}`)
	if code != 200 {
		t.Fatalf("run %d %s", code, runBody)
	}
	sched, _ := os.ReadFile(WebSchedulesPath(env.Cfg.Root))
	store, _ := os.ReadFile(env.Cfg.Root + "/" + issuesync.File)
	for _, s := range []string{string(body), string(runBody), string(sched), string(store)} {
		for _, bad := range []string{"ghp_", "github_pat_", "x-access-token"} {
			if strings.Contains(s, bad) {
				t.Fatalf("token %q leaked into %s", bad, s)
			}
		}
	}
	if !strings.Contains(string(store), `"private": true`) {
		t.Fatalf("not marked private: %s", store)
	}
}

func TestIssueSyncRunNeedsYes(t *testing.T) {
	env := issueSyncEnv(t)
	_, body := issueSyncServe(t, env, "POST", APIPrefix+"/issuesync", `{"repo":"a/b"}`)
	var r issuesync.Repo
	_ = json.Unmarshal(body, &r)
	if code, _ := issueSyncServe(t, env, "POST", APIPrefix+"/issuesync/"+r.ID+"/run", `{}`); code != 400 {
		t.Fatalf("run without yes %d", code)
	}
}
