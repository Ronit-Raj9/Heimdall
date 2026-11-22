package handlers

// Release policy editor (web parity lane L16, Govern). The policies/ folder of
// the org's project holds the release policies experiments reference
// (`policy: policies/default.yaml`). These routes list, read, validate, save
// and delete them through the project store, so every save is validated with
// the same parser the planner uses and lands atomically and versioned.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// PolicySummary is one policy file in the list.
type PolicySummary struct {
	File        string `json:"file"` // file name inside policies/, e.g. default.yaml
	Path        string `json:"path"` // project-relative path an experiment references
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Checks      int    `json:"checks"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error,omitempty"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

// PolicyList is GET /policies.
type PolicyList struct {
	Policies []PolicySummary `json:"policies"`
}

// PolicyDocument is one policy file: its raw YAML and, when it parses, the
// decoded policy.
type PolicyDocument struct {
	File   string          `json:"file"`
	Path   string          `json:"path"`
	YAML   string          `json:"yaml"`
	SHA256 string          `json:"sha256"`
	Policy *schemas.Policy `json:"policy,omitempty"`
	Valid  bool            `json:"valid"`
	Error  string          `json:"error,omitempty"`
}

// PolicySaveRequest is the body of PUT /policies/{name} and POST
// /policies/validate. Exactly one of YAML (the YAML tab) or Policy (the form)
// is set. IfMatch is the sha256 the editor loaded; a mismatch answers 409.
type PolicySaveRequest struct {
	YAML    string          `json:"yaml,omitempty"`
	Policy  *schemas.Policy `json:"policy,omitempty"`
	IfMatch string          `json:"if_match,omitempty"`
	Note    string          `json:"note,omitempty"`
	// CreateOnly saves a new rule set only when no file of that name exists
	// (409 otherwise), so a new set never replaces an existing one (N6).
	CreateOnly bool `json:"create_only,omitempty"`
}

// PolicyValidation is POST /policies/validate: whether the document parses,
// the decoded policy and the YAML that would be saved.
type PolicyValidation struct {
	Valid  bool            `json:"valid"`
	Error  string          `json:"error,omitempty"`
	YAML   string          `json:"yaml"`
	Policy *schemas.Policy `json:"policy,omitempty"`
}

var policyFileRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}\.ya?ml$`)

// PoliciesRouteDocs describe the policy editor routes.
var PoliciesRouteDocs = []RouteDoc{
	{Method: "GET", Path: APIPrefix + "/policies", Tag: "policies", Summary: "Release policies in the project's policies/ folder, each with its name, check count and whether it parses", Response: PolicyList{}},
	{Method: "POST", Path: APIPrefix + "/policies/validate", Tag: "policies", Summary: "Validate a release policy (YAML or the form's JSON) without saving; answers the decoded policy and the YAML a save would write",
		Auth: AuthWrite, Request: PolicySaveRequest{}, Response: PolicyValidation{}},
	{Method: "GET", Path: APIPrefix + "/policies/{name}", Tag: "policies", Summary: "One release policy: raw YAML, sha256 and the decoded policy", Response: PolicyDocument{}},
	{Method: "PUT", Path: APIPrefix + "/policies/{name}", Tag: "policies", Summary: "Create or replace a release policy after validating it with the planner's parser; if_match guards against overwriting a newer edit (409); create_only refuses a file name that already exists (409)",
		Auth: AuthWrite, Request: PolicySaveRequest{}, Response: PolicyDocument{}},
	{Method: "DELETE", Path: APIPrefix + "/policies/{name}", Tag: "policies", Summary: "Delete a release policy file", Auth: AuthWrite, NoContent: true},
}

func init() {
	APIRoutes = append(APIRoutes, PoliciesRouteDocs...)
	RegisterOrgArea("policies", func(env OrgEnv, r *router.Router) {
		h := policiesHandler{mode: env.Mode, store: env.Services.Projects}
		p := APIPrefix + "/policies"
		r.GET(p, env.R(h.list))
		r.POST(p+"/validate", env.W(h.validate))
		r.GET(p+"/{name}", env.R(h.get))
		r.PUT(p+"/{name}", env.W(h.put))
		r.DELETE(p+"/{name}", env.W(h.del))
	})
}

type policiesHandler struct {
	mode  schemas.Mode
	store projectfs.Store
}

func (h policiesHandler) ready(ctx *fasthttp.RequestCtx) bool {
	if h.store == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"the project file store is not configured on this server, so policies cannot be edited here; edit policies/*.yaml in the project folder instead")
		return false
	}
	return true
}

func (h policiesHandler) name(ctx *fasthttp.RequestCtx) (string, bool) {
	n, _ := ctx.UserValue("name").(string)
	if !policyFileRe.MatchString(n) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"policy file name must be lower case letters, digits, '.', '_' or '-' and end in .yaml, e.g. my-policy.yaml")
		return "", false
	}
	return n, true
}

func (h policiesHandler) list(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	entries, err := h.store.List(ctx, projectfs.AreaPolicies, "", false)
	if err != nil && !errors.Is(err, projectfs.ErrNotFound) {
		h.storeErr(ctx, err)
		return
	}
	out := PolicyList{Policies: []PolicySummary{}}
	for _, e := range entries {
		file := e.Path[strings.LastIndex(e.Path, "/")+1:]
		if e.Dir || !policyFileRe.MatchString(file) {
			continue
		}
		raw, ent, err := h.store.Read(ctx, projectfs.AreaPolicies, file)
		if err != nil {
			continue
		}
		s := PolicySummary{File: file, Path: "policies/" + file, SHA256: policy.Hash(raw), Size: ent.Size}
		if p, perr := policy.Parse(raw, s.Path); perr != nil {
			s.Error = perr.Error()
		} else {
			s.Valid, s.Name, s.Description, s.Checks = true, p.Name, p.Description, len(p.Checks)
		}
		out.Policies = append(out.Policies, s)
	}
	sort.Slice(out.Policies, func(i, j int) bool { return out.Policies[i].File < out.Policies[j].File })
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h policiesHandler) get(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	file, ok := h.name(ctx)
	if !ok {
		return
	}
	raw, _, err := h.store.Read(ctx, projectfs.AreaPolicies, file)
	if err != nil {
		h.storeErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, document(file, raw))
}

func (h policiesHandler) validate(ctx *fasthttp.RequestCtx) {
	req, ok := h.decode(ctx)
	if !ok {
		return
	}
	raw, err := policyBytes(req)
	if err != nil {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, PolicyValidation{Error: err.Error()})
		return
	}
	v := PolicyValidation{YAML: string(raw)}
	if p, perr := policy.Parse(raw, "policy"); perr != nil {
		v.Error = perr.Error()
	} else {
		v.Valid, v.Policy = true, p
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, v)
}

func (h policiesHandler) put(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	file, ok := h.name(ctx)
	if !ok {
		return
	}
	req, ok := h.decode(ctx)
	if !ok {
		return
	}
	raw, err := policyBytes(req)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if _, perr := policy.Parse(raw, "policies/"+file); perr != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "policy not saved: "+perr.Error())
		return
	}
	if req.CreateOnly { // N6: a new rule set never saves over an existing file
		_, _, rerr := h.store.Read(ctx, projectfs.AreaPolicies, file)
		switch {
		case rerr == nil:
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
				fmt.Sprintf("A rule set named %s already exists. Pick another file name.", strings.TrimSuffix(strings.TrimSuffix(file, ".yaml"), ".yml")))
			return
		case !errors.Is(rerr, projectfs.ErrNotFound):
			h.storeErr(ctx, rerr)
			return
		}
	}
	note := req.Note
	if note == "" {
		note = "policy editor"
	}
	if _, err := h.store.Write(ctx, projectfs.AreaPolicies, file, raw, projectfs.WriteOpts{Actor: policiesActor(ctx), Note: note, IfMatch: req.IfMatch}); err != nil {
		h.storeErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, document(file, raw))
}

func (h policiesHandler) del(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	file, ok := h.name(ctx)
	if !ok {
		return
	}
	if err := h.store.Delete(ctx, projectfs.AreaPolicies, file, projectfs.WriteOpts{Actor: policiesActor(ctx), Note: "policy editor delete"}); err != nil {
		h.storeErr(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h policiesHandler) decode(ctx *fasthttp.RequestCtx) (PolicySaveRequest, bool) {
	var req PolicySaveRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be JSON: {\"yaml\": \"...\"} or {\"policy\": {...}}")
		return req, false
	}
	return req, true
}

func (h policiesHandler) storeErr(ctx *fasthttp.RequestCtx, err error) {
	switch {
	case errors.Is(err, projectfs.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no such policy file in policies/")
	case errors.Is(err, projectfs.ErrConflict):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "the policy changed since you opened it; reload it, then apply your edit again")
	case errors.Is(err, projectfs.ErrTooLarge):
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "policy file is larger than 1 MiB")
	case errors.Is(err, projectfs.ErrOutside):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "path is outside the policies folder")
	case errors.Is(err, projectfs.ErrInvalid):
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "policy store error")
	}
}

func policiesActor(ctx *fasthttp.RequestCtx) string {
	if p := auth.PrincipalFrom(ctx); p != nil {
		if p.Email != "" {
			return p.Email
		}
		if p.UserID != "" {
			return p.UserID
		}
	}
	return "web"
}

func document(file string, raw []byte) PolicyDocument {
	d := PolicyDocument{File: file, Path: "policies/" + file, YAML: string(raw), SHA256: policy.Hash(raw)}
	if p, err := policy.Parse(raw, d.Path); err != nil {
		d.Error = err.Error()
	} else {
		d.Valid, d.Policy = true, p
	}
	return d
}

// policyBytes is the YAML a request saves: the YAML tab's text as is, or the
// form's policy rendered with empty optional fields left out.
func policyBytes(req PolicySaveRequest) ([]byte, error) {
	switch {
	case req.Policy != nil && strings.TrimSpace(req.YAML) != "":
		return nil, fmt.Errorf("send either yaml or policy, not both")
	case req.Policy != nil:
		return renderPolicyYAML(req.Policy)
	case strings.TrimSpace(req.YAML) != "":
		if len(req.YAML) > projectfs.MaxFileBytes {
			return nil, fmt.Errorf("policy is larger than 1 MiB")
		}
		return []byte(req.YAML), nil
	default:
		return nil, fmt.Errorf("the policy is empty: add a name and at least one check")
	}
}

type policyCheckYAML struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description,omitempty"`
	Metric      string   `yaml:"metric"`
	Condition   string   `yaml:"condition,omitempty"`
	Arm         string   `yaml:"arm,omitempty"`
	Rule        string   `yaml:"rule,omitempty"`
	Max         *float64 `yaml:"max,omitempty"`
	Min         *float64 `yaml:"min,omitempty"`
	Equals      *float64 `yaml:"equals,omitempty"`
	MinN        int      `yaml:"min_n,omitempty"`
	Severity    string   `yaml:"severity,omitempty"`
}

type policyYAML struct {
	PolicyVersion    int               `yaml:"policy_version"`
	Name             string            `yaml:"name"`
	Description      string            `yaml:"description,omitempty"`
	OnUnknown        string            `yaml:"on_unknown,omitempty"`
	Checks           []policyCheckYAML `yaml:"checks"`
	JudgeCalibration map[string]any    `yaml:"judge_calibration,omitempty"`
}

func renderPolicyYAML(p *schemas.Policy) ([]byte, error) {
	doc := policyYAML{PolicyVersion: p.PolicyVersion, Name: p.Name, Description: p.Description, OnUnknown: p.OnUnknown}
	if p.JudgeCalibration != nil {
		// Through JSON so unset thresholds are left out instead of written as null.
		b, err := json.Marshal(p.JudgeCalibration)
		if err == nil {
			err = json.Unmarshal(b, &doc.JudgeCalibration)
		}
		if err != nil {
			return nil, fmt.Errorf("render policy: %w", err)
		}
	}
	if doc.PolicyVersion == 0 {
		doc.PolicyVersion = 1
	}
	doc.Checks = make([]policyCheckYAML, 0, len(p.Checks))
	for _, c := range p.Checks {
		doc.Checks = append(doc.Checks, policyCheckYAML(c))
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("render policy: %w", err)
	}
	return append([]byte("# Release policy, saved from the Heimdall policy editor.\n"), b...), nil
}
