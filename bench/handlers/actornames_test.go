package handlers

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

var (
	actorMember = &auth.Principal{Kind: auth.KindUser, UserID: "usr_2", OrgID: "org_a", Role: auth.RoleMember, Email: "bo@example.com"}
	actorAdmin  = &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleAdmin, Email: "ada@example.com"}
)

func TestActorNamesShow(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	member := auth.WithPrincipal(context.Background(), actorMember)
	admin := auth.WithPrincipal(context.Background(), actorAdmin)
	cases := []struct {
		ctx               context.Context
		actor, keep, name string
	}{
		{member, "usr_1", "usr_1", "Ada"},
		{member, "ada@example.com", "", "Ada"},
		{member, "Cy@Other.io", "", "Cy"},
		{member, "usr_9", "usr_9", ""},
		{member, "", "", ""},
		{admin, "ada@example.com", "ada@example.com", "Ada"},
		{admin, "cy@other.io", "cy@other.io", "cy"},
	}
	for _, c := range cases {
		keep, name := newActorNames(c.ctx).show(c.actor)
		if keep != c.keep || name != c.name {
			t.Errorf("show(%q) = %q, %q; want %q, %q", c.actor, keep, name, c.keep, c.name)
		}
	}
}

// A file's versions name their actor; a member never gets an email.
func TestProjectFileVersionsNameActor(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	store, err := projectfs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.Write(ctx, projectfs.AreaUploads, "notes/a.txt", []byte("one"), projectfs.WriteOpts{Actor: "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
	r := router.New()
	orgAreasMu.Lock()
	area := orgAreas["projectfiles"]
	orgAreasMu.Unlock()
	area(OrgEnv{Mode: testMode, Services: Services{Projects: store}}, r)
	// A served request: the projects store reads the request context's Done.
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		p := actorMember
		if string(ctx.Request.Header.Peek("X-Test-Role")) == "admin" {
			p = actorAdmin
		}
		auth.SetPrincipal(ctx, p)
		r.Handler(ctx)
	}}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	c := &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
	for _, tc := range []struct {
		p         *auth.Principal
		wantActor string
	}{{actorMember, ""}, {actorAdmin, "ada@example.com"}} {
		resp := c.do(t, "GET", APIPrefix+"/project/versions/uploads/notes/a.txt", nil, map[string]string{"X-Test-Role": string(tc.p.Role)})
		code, body := resp.StatusCode(), resp.Body()
		var vs ProjectFileVersions
		if code != 200 || json.Unmarshal(body, &vs) != nil || len(vs.Versions) != 1 {
			t.Fatalf("%s: %d %s", tc.p.Role, code, body)
		}
		if v := vs.Versions[0]; v.ActorName != "Ada" || v.Actor != tc.wantActor {
			t.Fatalf("%s: version %+v", tc.p.Role, v)
		}
		if tc.wantActor == "" && strings.Contains(string(body), "@") {
			t.Fatalf("member sees an email: %s", body)
		}
	}
}

type actorJobsAPI struct {
	JobsAPI
	j jobs.Job
}

func (a actorJobsAPI) Get(context.Context, string, string) (*jobs.Job, error) {
	j := a.j
	return &j, nil
}

// The job page names who started the job; a member never gets an email.
func TestJobGetNamesCreator(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	h := NewJobsHandler(testMode, actorJobsAPI{j: jobs.Job{ID: "job_1", OrgID: "org_a", Kind: "demo.echo", CreatedBy: "ada@example.com", CreatedAt: time.Now()}}, passThrough)
	r := router.New()
	r.GET(APIPrefix+"/jobs/{id}", h.get)
	code, body := abCallAs(r, actorMember, "GET", APIPrefix+"/jobs/job_1", nil)
	var j jobs.Job
	if code != 200 || json.Unmarshal(body, &j) != nil || j.CreatedByName != "Ada" || j.CreatedBy != "" {
		t.Fatalf("member: %d %s", code, body)
	}
	code, body = abCallAs(r, actorAdmin, "GET", APIPrefix+"/jobs/job_1", nil)
	j = jobs.Job{}
	if code != 200 || json.Unmarshal(body, &j) != nil || j.CreatedByName != "Ada" || j.CreatedBy != "ada@example.com" {
		t.Fatalf("admin: %d %s", code, body)
	}
}
