package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

var (
	errWkInvalid  = errors.New("invalid worker request")
	errWkNotFound = errors.New("worker connection not found")
)

type fakeWorkerConns struct {
	orgs   []string
	create schemas.WorkerConnectionCreate
	err    error
	def    string
}

func (f *fakeWorkerConns) seen(org string) { f.orgs = append(f.orgs, org) }

func (f *fakeWorkerConns) conn(org, id string) *schemas.WorkerConnection {
	return &schemas.WorkerConnection{ID: id, Name: "rl-1", Pool: "rl-1", Platform: "runloop", Status: schemas.WorkerConnReady}
}

func (f *fakeWorkerConns) List(_ context.Context, org string) (*schemas.WorkerConnectionsList, error) {
	f.seen(org)
	return &schemas.WorkerConnectionsList{Connections: []schemas.WorkerConnection{*f.conn(org, "wkc_1")}, DefaultWorker: f.def}, f.err
}
func (f *fakeWorkerConns) Get(_ context.Context, org, id string) (*schemas.WorkerConnection, error) {
	f.seen(org)
	if id == "wkc_missing" {
		return nil, fmt.Errorf("worker %s: %w", id, errWkNotFound)
	}
	return f.conn(org, id), f.err
}
func (f *fakeWorkerConns) Create(_ context.Context, org, _ string, in schemas.WorkerConnectionCreate) (*schemas.WorkerConnectionCreated, error) {
	f.seen(org)
	f.create = in
	if in.Name == "bad" {
		return nil, fmt.Errorf("%w: name", errWkInvalid)
	}
	return &schemas.WorkerConnectionCreated{Connection: *f.conn(org, "wkc_2"), Install: "curl ... HEIMDALL_RUNNER_TOKEN=hmr_secret sh"}, nil
}
func (f *fakeWorkerConns) Check(_ context.Context, org, id string) (*schemas.WorkerConnection, error) {
	f.seen(org)
	return f.conn(org, id), f.err
}
func (f *fakeWorkerConns) Start(_ context.Context, org, id string) (*schemas.WorkerConnection, error) {
	f.seen(org)
	return f.conn(org, id), f.err
}
func (f *fakeWorkerConns) Stop(_ context.Context, org, id string) (*schemas.WorkerConnection, error) {
	f.seen(org)
	return f.conn(org, id), f.err
}
func (f *fakeWorkerConns) Delete(_ context.Context, org, id string) (*schemas.WorkerConnection, error) {
	f.seen(org)
	return f.conn(org, id), f.err
}
func (f *fakeWorkerConns) InstallCommand(_ context.Context, org, _ string) (string, error) {
	f.seen(org)
	return "curl ... HEIMDALL_RUNNER_TOKEN=hmr_secret sh", f.err
}
func (f *fakeWorkerConns) SetDefaultWorker(_ context.Context, org, _ string, pool string) (string, error) {
	f.seen(org)
	f.def = pool
	return pool, f.err
}
func (f *fakeWorkerConns) KubeManifest(ns string) string { return "namespace: " + ns + "\n" }

func TestWorkerConnsRoutes(t *testing.T) {
	api := &fakeWorkerConns{}
	var audits []auditRow
	h := NewWorkerConnsHandler(schemas.ModeLive, api, WorkerConnErrors{Invalid: errWkInvalid, NotFound: errWkNotFound},
		func(_ *fasthttp.RequestCtx, org, action, tt, tid string, details map[string]any) {
			if strings.Contains(fmt.Sprint(details), "hmr_") {
				t.Fatal("a token reached the audit log")
			}
			audits = append(audits, auditRow{org, action, tt, tid})
		})
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleAdmin}
	viewer := &auth.Principal{Kind: auth.KindUser, UserID: "usr_2", OrgID: "org_a", Role: auth.RoleViewer}
	runner := &auth.Principal{Kind: auth.KindRunner, OrgID: "org_a"}

	if r := call(t, reg, nil, "GET", "/api/bench/workers/connections", ""); r.StatusCode() != 401 {
		t.Fatalf("anonymous list %d", r.StatusCode())
	}
	if r := call(t, reg, runner, "GET", "/api/bench/workers/connections", ""); r.StatusCode() != 401 {
		t.Fatalf("runner list %d", r.StatusCode())
	}
	r := call(t, reg, viewer, "GET", "/api/bench/workers/connections", "")
	var list schemas.WorkerConnectionsList
	if r.StatusCode() != 200 || json.Unmarshal(r.Body(), &list) != nil || len(list.Connections) != 1 {
		t.Fatalf("viewer list %d %s", r.StatusCode(), r.Body())
	}
	for _, w := range []struct{ method, uri, body string }{
		{"POST", "/api/bench/workers/connections", `{"name":"x","platform":"vm"}`},
		{"POST", "/api/bench/workers/connections/wkc_1/start", ""},
		{"POST", "/api/bench/workers/connections/wkc_1/install-command", ""},
		{"PUT", "/api/bench/workers/default", `{"worker":"rl-1"}`},
	} {
		if r := call(t, reg, viewer, w.method, w.uri, w.body); r.StatusCode() != 403 {
			t.Fatalf("viewer %s %s: %d", w.method, w.uri, r.StatusCode())
		}
	}
	r = call(t, reg, admin, "POST", "/api/bench/workers/connections", `{"name":"lab","platform":"vm"}`)
	if r.StatusCode() != 201 || string(r.Header.Peek("Cache-Control")) != "no-store" || api.create.Platform != "vm" {
		t.Fatalf("create %d %s", r.StatusCode(), r.Body())
	}
	if r := call(t, reg, admin, "POST", "/api/bench/workers/connections", `{"name":"x","nope":1}`); r.StatusCode() != 400 {
		t.Fatalf("unknown field %d", r.StatusCode())
	}
	if r := call(t, reg, admin, "POST", "/api/bench/workers/connections", `{"name":"bad","platform":"vm"}`); r.StatusCode() != 400 {
		t.Fatalf("invalid %d", r.StatusCode())
	}
	if r := call(t, reg, viewer, "GET", "/api/bench/workers/connections/wkc_missing", ""); r.StatusCode() != 404 {
		t.Fatalf("missing %d", r.StatusCode())
	}
	for _, a := range []string{"check", "start", "stop", "remove", "install-command"} {
		if r := call(t, reg, admin, "POST", "/api/bench/workers/connections/wkc_1/"+a, ""); r.StatusCode() != 200 {
			t.Fatalf("%s %d %s", a, r.StatusCode(), r.Body())
		}
	}
	if r := call(t, reg, admin, "PUT", "/api/bench/workers/default", `{"worker":"rl-1"}`); r.StatusCode() != 200 || api.def != "rl-1" {
		t.Fatalf("default %d", r.StatusCode())
	}
	r = call(t, reg, viewer, "GET", "/api/bench/workers/kubernetes-manifest?namespace=acme", "")
	if r.StatusCode() != 200 || !strings.Contains(string(r.Body()), "namespace: acme") {
		t.Fatalf("manifest %d %s", r.StatusCode(), r.Body())
	}
	if r := call(t, reg, viewer, "GET", "/api/bench/workers/kubernetes-manifest?namespace=Bad_NS", ""); r.StatusCode() != 400 {
		t.Fatalf("bad namespace %d", r.StatusCode())
	}
	for _, o := range api.orgs {
		if o != "org_a" {
			t.Fatalf("call for org %q", o)
		}
	}
	actions := map[string]bool{}
	for _, a := range audits {
		actions[a.action] = true
	}
	for _, want := range []string{"worker.connect", "worker.start", "worker.stop", "worker.remove", "worker.install_command", "worker.default"} {
		if !actions[want] {
			t.Fatalf("no audit %s in %v", want, audits)
		}
	}
	if actions["worker.check"] {
		t.Fatal("a check is audited")
	}
	// Local mode: 503.
	off := NewWorkerConnsHandler(schemas.ModeLive, nil, WorkerConnErrors{}, nil)
	if r := call(t, func(r *router.Router) { off.RegisterRoutes(r) }, admin, "GET", "/api/bench/workers/connections", ""); r.StatusCode() != 503 {
		t.Fatalf("local %d", r.StatusCode())
	}
}

func TestInstallScriptWithDefaults(t *testing.T) {
	script := []byte("#!/bin/sh\nHEIMDALL_DEFAULT_SERVER=\"\"\nHEIMDALL_DEFAULT_REPO=\"\"\nHEIMDALL_DEFAULT_REF=\"\"\nrest\n")
	got := string(InstallScriptWithDefaults(script, "https://heimdall.example.com/", "https://github.com/acme/bifrost.git", "0123abc"))
	for _, want := range []string{"\nHEIMDALL_DEFAULT_SERVER='https://heimdall.example.com'\n", "\nHEIMDALL_DEFAULT_REPO='https://github.com/acme/bifrost.git'\n", "\nHEIMDALL_DEFAULT_REF='0123abc'\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lacks %q:\n%s", want, got)
		}
	}
	evil := string(InstallScriptWithDefaults(script, "https://x.example.com/'; rm -rf / #", "https://ok.example.com/r.git$(id)", "../x"))
	if evil != string(script) {
		t.Fatalf("unsafe values written:\n%s", evil)
	}
}

func TestEvaluationWorkerResolution(t *testing.T) {
	defer evaluationPoolResolver.Store((func(context.Context, string, string) (string, error))(nil))
	evaluationPoolResolver.Store((func(context.Context, string, string) (string, error))(nil))
	spec := &schemas.EvaluationSpec{Worker: "rl-1"}
	if err := resolveEvaluationWorker(context.Background(), "org_a", spec); err == nil || statusOf(err) != 400 {
		t.Fatalf("local mode with a worker: %v", err)
	}
	spec = &schemas.EvaluationSpec{Worker: "shared"}
	if err := resolveEvaluationWorker(context.Background(), "org_a", spec); err != nil || spec.Worker != "" {
		t.Fatalf("shared locally: %v %q", err, spec.Worker)
	}
	SetEvaluationPoolResolver(func(_ context.Context, org, w string) (string, error) {
		if org != "org_a" {
			return "", errors.New("wrong org")
		}
		switch w {
		case "":
			return "fly-1", nil // the org's default worker
		case "ghost":
			return "", errors.New("no worker named ghost")
		}
		return w, nil
	})
	spec = &schemas.EvaluationSpec{}
	if err := resolveEvaluationWorker(context.Background(), "org_a", spec); err != nil || spec.Worker != "fly-1" {
		t.Fatalf("default: %v %q", err, spec.Worker)
	}
	spec = &schemas.EvaluationSpec{Worker: "ghost"}
	if err := resolveEvaluationWorker(context.Background(), "org_a", spec); err == nil || statusOf(err) != 400 {
		t.Fatalf("ghost: %v", err)
	}
	body, err := appendRunnerPool([]byte("name: x\n"), "fly-1")
	if err != nil || string(body) != "name: x\nrunner:\n  pool: fly-1\n" {
		t.Fatalf("pool block %q %v", body, err)
	}
	if body, _ := appendRunnerPool([]byte("name: x\n"), ""); string(body) != "name: x\n" {
		t.Fatalf("shared worker added a block: %q", body)
	}
	if _, err := appendRunnerPool(nil, "a b"); err == nil {
		t.Fatal("bad pool accepted")
	}
}
