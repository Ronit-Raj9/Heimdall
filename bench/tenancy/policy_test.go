package tenancy

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
)

func status(err error) int {
	if err == nil {
		return 200
	}
	var d *Denied
	if errors.As(err, &d) {
		return d.Status
	}
	return 500
}

func TestAuthorizeMatrix(t *testing.T) {
	orgA := &Org{ID: "org_a"}
	demo := &Org{ID: "org_demo", PublicRead: true}
	user := func(org string, role auth.Role) *auth.Principal {
		return &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: org, Role: role}
	}
	token := &auth.Principal{Kind: auth.KindToken, TokenID: "tok_1", OrgID: "org_a", Role: auth.RoleMember}
	runnerA := &auth.Principal{Kind: auth.KindRunner, TokenID: "rnr_1", OrgID: "org_a"}
	anon := auth.Anonymous()
	anonDemo := &auth.Principal{Kind: auth.KindAnonymous, OrgID: "org_demo"}
	nonMember := &auth.Principal{Kind: auth.KindUser, UserID: "usr_2", OrgID: "org_demo"}

	tests := []struct {
		name   string
		rule   Rule
		method string
		p      *auth.Principal
		target *Org
		want   int
	}{
		{"viewer reads", org(viewer), "GET", user("org_a", viewer), orgA, 200},
		{"viewer may not write", org(member), "POST", user("org_a", viewer), orgA, 403},
		{"member writes", org(member), "POST", user("org_a", member), orgA, 200},
		{"member may not admin", org(admin), "POST", user("org_a", member), orgA, 403},
		{"admin admins", org(admin), "POST", user("org_a", admin), orgA, 200},
		{"admin may not owner", org(owner), "POST", user("org_a", admin), orgA, 403},
		{"other org's principal", org(viewer), "GET", user("org_b", owner), orgA, 403},
		{"token reads", org(viewer), "GET", token, orgA, 200},
		{"token capped at its role", org(admin), "GET", token, orgA, 403},
		{"anonymous on private org", org(viewer), "GET", anon, orgA, 401},
		{"anonymous reads public org", org(viewer), "GET", anonDemo, demo, 200},
		{"anonymous head public org", org(viewer), "HEAD", anonDemo, demo, 200},
		{"anonymous may not write public org", org(member), "POST", anonDemo, demo, 401},
		{"anonymous may not read admin pages", org(admin), "GET", anonDemo, demo, 401},
		{"non member reads public org", org(viewer), "GET", nonMember, demo, 200},
		{"non member may not write public org", org(member), "POST", nonMember, demo, 403},
		{"runner on worker route", runner(true), "POST", runnerA, orgA, 200},
		{"runner of other org", runner(true), "POST", runnerA, &Org{ID: "org_b"}, 403},
		{"runner on data route", org(viewer), "GET", runnerA, orgA, 403},
		{"user on worker route", runner(true), "POST", user("org_a", owner), orgA, 403},
		{"anonymous on worker route", runner(true), "POST", anon, orgA, 401},
		{"runner on signed-in route", signedIn(), "GET", runnerA, nil, 403},
		{"token on user-only route", userOnly(), "POST", token, nil, 403},
		{"anonymous on signed-in route", signedIn(), "GET", anon, nil, 401},
		{"public route", public(), "POST", anon, nil, 200},
		{"deny", deny("no"), "GET", user("org_a", owner), orgA, 403},
		{"zero rule denies", Rule{}, "GET", user("org_a", owner), orgA, 403},
		{"local principal refused", org(viewer), "GET", auth.Local(), &Org{ID: LocalOrgID}, 403},
		{"no org", org(viewer), "GET", user("", owner), nil, 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status(Authorize(tt.rule, tt.method, tt.p, tt.target)); got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPolicyDefaults(t *testing.T) {
	p := DefaultPolicy()
	if r, ok := p.OrgRule("POST", api+"/brand-new"); ok || r.Access != AccessDeny {
		t.Errorf("unknown org write: %+v, %v", r, ok)
	}
	if r, ok := p.OrgRule("GET", api+"/brand-new"); ok || r.Access != AccessOrg || r.Min != viewer {
		t.Errorf("unknown org read: %+v, %v", r, ok)
	}
	if r, ok := p.PlatformRule("GET", api+"/brand-new"); ok || r.Access != AccessDeny {
		t.Errorf("unknown platform read: %+v, %v", r, ok)
	}
	if r, _ := p.OrgRule("HEAD", api+"/runs/{id}"); r.Access != AccessOrg {
		t.Errorf("HEAD not filed under GET: %+v", r)
	}
	// Every GET of the org table is a viewer read, every write needs at
	// least member or is runner-only: the rule of the contract.
	for k, r := range p.Org {
		method, _, _ := strings.Cut(k, " ")
		switch {
		case r.Access == AccessDeny:
		case method == "GET" && r.Access == AccessOrg && r.Min != viewer:
			t.Errorf("%s: GET needs %s", k, r.Min)
		case method != "GET" && r.Access == AccessOrg && !r.Min.AtLeast(member):
			t.Errorf("%s: write open to %s", k, r.Min)
		case r.Access == AccessPublic || r.Access == AccessSignedIn || r.Access == AccessUser:
			t.Errorf("%s: org route without an org", k)
		}
	}
}

// fakeDir is an in-memory Directory.
type fakeDir struct {
	orgs    []Org
	members map[string]auth.Role // org/user
}

func (d fakeDir) OrgByID(_ context.Context, id string) (Org, error) {
	for _, o := range d.orgs {
		if o.ID == id {
			return o, nil
		}
	}
	return Org{}, ErrOrgNotFound
}

func (d fakeDir) OrgBySlug(_ context.Context, slug string) (Org, error) {
	for _, o := range d.orgs {
		if o.Slug == slug {
			return o, nil
		}
	}
	return Org{}, ErrOrgNotFound
}

func (d fakeDir) MemberRole(_ context.Context, orgID, userID string) (auth.Role, error) {
	if r, ok := d.members[orgID+"/"+userID]; ok {
		return r, nil
	}
	return "", ErrNotMember
}

func TestResolve(t *testing.T) {
	dir := fakeDir{
		orgs: []Org{{ID: "org_a", Slug: "acme"}, {ID: "org_b", Slug: "beta"}, {ID: "org_d", Slug: "demo", PublicRead: true}},
		members: map[string]auth.Role{
			"org_a/usr_1": auth.RoleAdmin, "org_b/usr_1": auth.RoleViewer,
		},
	}
	u := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleAdmin}
	stranger := &auth.Principal{Kind: auth.KindUser, UserID: "usr_9", OrgID: ""}
	tok := &auth.Principal{Kind: auth.KindToken, TokenID: "tok", OrgID: "org_a", Role: auth.RoleMember}
	run := &auth.Principal{Kind: auth.KindRunner, TokenID: "rnr", OrgID: "org_b"}
	tests := []struct {
		name         string
		rule         Rule
		p            *auth.Principal
		path, header string
		wantOrg      string
		wantRole     auth.Role
		wantStatus   int
	}{
		{"session org", org(viewer), u, "", "", "org_a", auth.RoleAdmin, 0},
		{"header switches by slug", org(viewer), u, "", "beta", "org_b", auth.RoleViewer, 0},
		{"header by id", org(viewer), u, "", "org_b", "org_b", auth.RoleViewer, 0},
		{"header to a non-member private org", org(viewer), stranger, "", "acme", "", "", 404},
		{"header to an unknown org", org(viewer), u, "", "nope", "", "", 404},
		{"user without org", org(viewer), stranger, "", "", "", "", 403},
		{"non member on public org", org(viewer), stranger, "", "demo", "org_d", "", 0},
		{"path org", pathOrg(admin), u, "beta", "", "org_b", auth.RoleViewer, 0},
		{"path org beats header", pathOrg(admin), u, "beta", "acme", "org_b", auth.RoleViewer, 0},
		{"token own org", org(viewer), tok, "", "", "org_a", auth.RoleMember, 0},
		{"token header to other org", org(viewer), tok, "", "beta", "", "", 404},
		{"token path to other org", pathOrg(viewer), tok, "org_b", "", "", "", 404},
		{"token header naming own org", org(viewer), tok, "", "acme", "org_a", auth.RoleMember, 0},
		{"runner own org", runner(true), run, "", "", "org_b", "", 0},
		{"runner header to other org", runner(true), run, "", "acme", "", "", 404},
		{"anonymous gets no default org", org(viewer), auth.Anonymous(), "", "", "", "", 401},
		{"anonymous public_read org", org(viewer), auth.Anonymous(), "", "demo", "", "", 401},
		{"anonymous public_read org by path", pathOrg(viewer), auth.Anonymous(), "demo", "", "", "", 401},
		{"anonymous private org", org(viewer), auth.Anonymous(), "", "acme", "", "", 401},
		{"anonymous unknown org", org(viewer), auth.Anonymous(), "", "zzz", "", "", 401},
		{"no org rule", signedIn(), u, "", "beta", "", auth.RoleAdmin, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, eff, err := Resolve(context.Background(), dir, tt.rule, tt.p, tt.path, tt.header)
			if tt.wantStatus != 0 {
				if got := status(err); got != tt.wantStatus {
					t.Fatalf("status %d, want %d (%v)", got, tt.wantStatus, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			gotOrg := ""
			if o != nil {
				gotOrg = o.ID
				if eff.OrgID != o.ID {
					t.Fatalf("effective principal org %q, target %q", eff.OrgID, o.ID)
				}
			}
			if gotOrg != tt.wantOrg || eff.Role != tt.wantRole {
				t.Fatalf("org %q role %q, want %q %q", gotOrg, eff.Role, tt.wantOrg, tt.wantRole)
			}
		})
	}
	if u.OrgID != "org_a" || u.Role != auth.RoleAdmin {
		t.Fatal("Resolve modified the caller's principal")
	}
}

func TestCheckExperimentPaths(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "agents"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "policies"), 0o755))
	must(os.WriteFile(filepath.Join(root, "agents", "a.yaml"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(root, "policies", "p.yaml"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(other, "secret.yaml"), []byte("x"), 0o644))
	must(os.Symlink(filepath.Join(other, "secret.yaml"), filepath.Join(root, "agents", "link.yaml")))
	ok := func() *schemas.Experiment {
		return &schemas.Experiment{Suite: "coding-core@1.0.0", Base: "agents/a.yaml", Policy: "policies/p.yaml",
			Arms: map[string]schemas.ArmSpec{"a": {Agent: "agents/a.yaml"}, "b": {}}}
	}
	if err := CheckExperimentPaths(root, ok()); err != nil {
		t.Fatalf("clean experiment refused: %v", err)
	}
	tests := map[string]func(e *schemas.Experiment){
		"suite path":      func(e *schemas.Experiment) { e.Suite = "../projects/org_b/benchmarks/x" },
		"suite absolute":  func(e *schemas.Experiment) { e.Suite = "/etc" },
		"suite dotdot":    func(e *schemas.Experiment) { e.Suite = ".." },
		"base absolute":   func(e *schemas.Experiment) { e.Base = filepath.Join(other, "secret.yaml") },
		"base escapes":    func(e *schemas.Experiment) { e.Base = "../" + filepath.Base(other) + "/secret.yaml" },
		"arm escapes":     func(e *schemas.Experiment) { e.Arms["b"] = schemas.ArmSpec{Agent: "../../x.yaml"} },
		"arm symlink out": func(e *schemas.Experiment) { e.Arms["b"] = schemas.ArmSpec{Agent: "agents/link.yaml"} },
		"policy escapes":  func(e *schemas.Experiment) { e.Policy = "../../../etc/passwd" },
		"compat escapes":  func(e *schemas.Experiment) { e.Compatibility = &schemas.CompatibilitySpec{Agent: "/etc/passwd"} },
		"matrix escapes":  func(e *schemas.Experiment) { e.Matrix = &schemas.MatrixSpec{Agents: []string{"../a.yaml"}} },
		"backslash":       func(e *schemas.Experiment) { e.Base = `agents\..\..\x` },
	}
	for name, mut := range tests {
		t.Run(name, func(t *testing.T) {
			e := ok()
			mut(e)
			if err := CheckExperimentPaths(root, e); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestStatusCodesAreHTTP(t *testing.T) {
	if unauthorized("x").Status != http.StatusUnauthorized || forbidden("x").Status != http.StatusForbidden || notFound("x").Status != http.StatusNotFound {
		t.Fatal("status mapping")
	}
}

// TestShareLinkAccess pins lane X14: a share route admits anyone on GET,
// resolves the org from the token, never writes, and refuses runners.
func TestShareLinkAccess(t *testing.T) {
	dir := shareDir{orgs: map[string]Org{"org_a": {ID: "org_a", Slug: "a", Name: "A"}}}
	rule := shareLink()
	o, eff, err := Resolve(context.Background(), dir, rule, auth.Anonymous(), "org_a.shr_0123456789abcdef0123.sig", "")
	if err != nil || o == nil || o.ID != "org_a" || eff.OrgID != "org_a" || eff.Kind != auth.KindAnonymous || eff.Role != "" {
		t.Fatalf("resolve %v %v %v", o, eff, err)
	}
	if err := Authorize(rule, "GET", eff, o); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := Authorize(rule, "POST", eff, o); err == nil {
		t.Fatal("share routes must refuse writes")
	}
	if err := Authorize(rule, "GET", &auth.Principal{Kind: auth.KindRunner, OrgID: "org_a"}, o); err == nil {
		t.Fatal("runner on a share route")
	}
	user := &auth.Principal{Kind: auth.KindUser, UserID: "usr_x", OrgID: "org_b", Role: auth.RoleOwner}
	if _, eff, err := Resolve(context.Background(), dir, rule, user, "org_a.x.y", ""); err != nil || eff.Kind != auth.KindAnonymous || eff.Role != "" {
		t.Fatalf("signed-in caller must read as anonymous: %v %v", eff, err)
	}
	for _, tok := range []string{"", "nodot", "Bad-Org.x.y", "org_missing.x.y"} {
		if _, _, err := Resolve(context.Background(), dir, rule, auth.Anonymous(), tok, ""); err == nil {
			t.Errorf("token %q resolved", tok)
		}
	}
}

type shareDir struct{ orgs map[string]Org }

func (d shareDir) OrgByID(_ context.Context, id string) (Org, error) {
	if o, ok := d.orgs[id]; ok {
		return o, nil
	}
	return Org{}, ErrOrgNotFound
}
func (d shareDir) OrgBySlug(context.Context, string) (Org, error) { return Org{}, ErrOrgNotFound }
func (d shareDir) MemberRole(context.Context, string, string) (auth.Role, error) {
	return "", ErrNotMember
}
