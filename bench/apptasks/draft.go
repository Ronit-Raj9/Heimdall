package apptasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Draft states.
const (
	StatusNew           = "new"            // spec and policy uploaded
	StatusRulesDrafted  = "rules_drafted"  // the assist listed the rules; a person approves them
	StatusRulesApproved = "rules_approved" // a person approved the rules
	StatusGenerated     = "generated"      // seed and tasks drafted and proved
	StatusWritten       = "written"        // tasks written into the benchmark, pending review
)

// Task review states of a written task.
const (
	ReviewPending  = "pending"
	ReviewAccepted = "accepted"
	ReviewRejected = "rejected"
)

// Dropped is a scenario that failed a gate.
type Dropped struct {
	RuleID string `json:"rule_id"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// Written is a task written into the benchmark.
type Written struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	RuleID     string    `json:"rule_id"`
	Checks     int       `json:"checks"`
	Summary    string    `json:"summary"`
	Review     string    `json:"review"`
	ReviewedBy string    `json:"reviewed_by,omitempty"`
	ReviewedAt time.Time `json:"reviewed_at,omitempty"`
}

// Draft is one generation: the uploads, the approved rules, the drafted
// seed and tasks, what each assist call cost, and what was written.
type Draft struct {
	ID         string    `json:"id"`
	Suite      string    `json:"suite"`
	App        string    `json:"app"`
	Title      string    `json:"title,omitempty"`
	SpecFile   string    `json:"spec_file"`
	PolicyFile string    `json:"policy_file"`
	Status     string    `json:"status"`
	CreatedBy  string    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	Model      *Pinned `json:"model,omitempty"`
	Rules      []Rule  `json:"rules"`
	ApprovedBy string  `json:"approved_by,omitempty"`

	Seed    State     `json:"seed,omitempty"`
	Judged  bool      `json:"judged"` // tasks keep reply rubrics, so the benchmark needs a pinned judge
	Plans   []*Plan   `json:"plans"`
	Dropped []Dropped `json:"dropped"`
	Written []Written `json:"written"`

	Calls    []CallRecord `json:"calls"`
	SpentUSD float64      `json:"spent_usd"`
	Warnings []string     `json:"warnings"`
}

// planFile keeps a plan's checks with it on disk (Plan.Checks is not in
// the API's JSON).
type planFile struct {
	*Plan
	ChecksYAML string `json:"checks_yaml"`
}

// MarshalJSON keeps the checks with each plan.
func (d *Draft) store() ([]byte, error) {
	type alias Draft
	aux := struct {
		*alias
		Plans []planFile `json:"plans"`
	}{alias: (*alias)(d)}
	for _, p := range d.Plans {
		aux.Plans = append(aux.Plans, planFile{Plan: p, ChecksYAML: string(p.Checks)})
	}
	return json.MarshalIndent(aux, "", "  ")
}

func loadDraft(b []byte) (*Draft, error) {
	type alias Draft
	var aux struct {
		alias
		Plans []planFile `json:"plans"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return nil, err
	}
	d := Draft(aux.alias)
	d.Plans = nil
	for _, pf := range aux.Plans {
		if pf.Plan == nil {
			continue
		}
		pf.Plan.Checks = []byte(pf.ChecksYAML)
		d.Plans = append(d.Plans, pf.Plan)
	}
	return &d, nil
}

// Store keeps drafts in a folder, one sub-folder per draft.
type Store struct{ Dir string }

// Folder is where a project keeps drafts, relative to its root.
const Folder = "uploads/app-tasks"

var (
	draftIDRE = regexp.MustCompile(`^ag_[0-9a-f]{12}$`)
	appIDRE   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// ValidDraftID reports whether id is a draft id.
func ValidDraftID(id string) bool { return draftIDRE.MatchString(id) }

func newDraftID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "ag_" + hex.EncodeToString(b[:])
}

func (s Store) dir(id string) (string, error) {
	if !ValidDraftID(id) {
		return "", fmt.Errorf("draft id %q is not valid", id)
	}
	return filepath.Join(s.Dir, id), nil
}

var (
	locksMu sync.Mutex
	locks   = map[string]*sync.Mutex{}
)

func (s Store) lock(id string) *sync.Mutex {
	k := filepath.Join(s.Dir, id)
	locksMu.Lock()
	defer locksMu.Unlock()
	l, ok := locks[k]
	if !ok {
		l = &sync.Mutex{}
		locks[k] = l
	}
	return l
}

// Lock holds a draft for a whole generation job; the returned func
// releases it.
func (s Store) Lock(id string) func() {
	l := s.lock(id)
	l.Lock()
	return l.Unlock
}

// ErrBusy is a draft a generation job holds.
var ErrBusy = errors.New("a generation job is working on this draft; wait for it to finish")

// TryLock holds a draft for a quick edit, or fails with ErrBusy.
func (s Store) TryLock(id string) (func(), error) {
	l := s.lock(id)
	if !l.TryLock() {
		return nil, ErrBusy
	}
	return l.Unlock, nil
}

// ErrNotFound is a missing draft.
var ErrNotFound = errors.New("draft not found")

// Save writes draft.json atomically.
func (s Store) Save(d *Draft) error {
	dir, err := s.dir(d.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	d.UpdatedAt = time.Now().UTC()
	b, err := d.store()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".draft-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), filepath.Join(dir, "draft.json"))
}

// Load reads a draft.
func (s Store) Load(id string) (*Draft, error) {
	dir, err := s.dir(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d, err := loadDraft(b)
	if err != nil {
		return nil, fmt.Errorf("draft %s: %v", id, err)
	}
	if d.ID != id {
		return nil, fmt.Errorf("draft %s: its file names %q", id, d.ID)
	}
	return d, nil
}

// List reads every draft, newest first.
func (s Store) List() ([]*Draft, error) {
	ents, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Draft
	for _, e := range ents {
		if !e.IsDir() || !ValidDraftID(e.Name()) {
			continue
		}
		if d, err := s.Load(e.Name()); err == nil {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Upload is what a person gives to start a draft.
type Upload struct {
	Suite      string
	App        string
	Title      string
	Spec       []byte
	SpecName   string
	Policy     []byte
	PolicyName string
	By         string
}

// Create checks the upload (the spec must make a mock; the policy must be
// text) and saves a new draft with its files.
func (s Store) Create(u Upload) (*Draft, *scaffold.MockSpec, error) {
	if !scaffold.ValidID(u.Suite) || len(u.Suite) > 64 {
		return nil, nil, fmt.Errorf("benchmark id %q: 1 to 64 lowercase letters, digits, '.', '_' or '-'", u.Suite)
	}
	if !appIDRE.MatchString(u.App) || u.App == "customer" {
		return nil, nil, fmt.Errorf("app id %q: lowercase letters, digits, '_' or '-', starting with a letter, at most 32, not customer", u.App)
	}
	if len([]rune(u.Title)) > 120 {
		return nil, nil, errors.New("title: at most 120 characters")
	}
	m, warns, err := scaffold.MockFromOpenAPI(u.App, u.Spec)
	if err != nil {
		return nil, nil, err
	}
	if len(m.Operations) == 0 {
		return nil, nil, errors.New("the OpenAPI file has no operations the app copy can serve")
	}
	if len(u.Policy) > MaxPolicyBytes {
		return nil, nil, fmt.Errorf("the policy is over %d KiB", MaxPolicyBytes>>10)
	}
	if err := policyText(u.Policy); err != nil {
		return nil, nil, err
	}
	ext := ".yaml"
	if strings.HasSuffix(strings.ToLower(u.SpecName), ".json") || strings.HasPrefix(strings.TrimSpace(string(u.Spec)), "{") {
		ext = ".json"
	}
	d := &Draft{ID: newDraftID(), Suite: u.Suite, App: u.App, Title: strings.TrimSpace(u.Title), SpecFile: "openapi" + ext,
		PolicyFile: "policy.md", Status: StatusNew, CreatedBy: u.By, CreatedAt: time.Now().UTC(),
		Rules: []Rule{}, Plans: []*Plan{}, Dropped: []Dropped{}, Written: []Written{}, Calls: []CallRecord{}, Warnings: warns}
	if d.Warnings == nil {
		d.Warnings = []string{}
	}
	if d.Title == "" {
		d.Title = m.Title
	}
	dir, _ := s.dir(d.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, d.SpecFile), u.Spec, 0o644); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, d.PolicyFile), u.Policy, 0o644); err != nil {
		return nil, nil, err
	}
	if err := s.Save(d); err != nil {
		return nil, nil, err
	}
	return d, m, nil
}

// policyText refuses a policy that is not readable text (a PDF must be
// pasted as text or Markdown).
func policyText(b []byte) error {
	t := strings.TrimSpace(string(b))
	if t == "" {
		return errors.New("policy: paste or upload the policy as text or Markdown")
	}
	if strings.HasPrefix(t, "%PDF") {
		return errors.New("policy: this is a PDF; paste its text or upload it as Markdown")
	}
	if strings.ContainsRune(t, 0) {
		return errors.New("policy: the file is not text")
	}
	return nil
}

// Files reads a draft's spec and policy.
func (s Store) Files(d *Draft) (spec, policy []byte, err error) {
	dir, err := s.dir(d.ID)
	if err != nil {
		return nil, nil, err
	}
	if spec, err = os.ReadFile(filepath.Join(dir, d.SpecFile)); err != nil {
		return nil, nil, err
	}
	if policy, err = os.ReadFile(filepath.Join(dir, d.PolicyFile)); err != nil {
		return nil, nil, err
	}
	return spec, policy, nil
}

// Mock derives the draft's mock from its stored spec.
func (s Store) Mock(d *Draft) (*scaffold.MockSpec, []byte, error) {
	spec, _, err := s.Files(d)
	if err != nil {
		return nil, nil, err
	}
	m, _, err := scaffold.MockFromOpenAPI(d.App, spec)
	return m, spec, err
}

// ApproveRules stores a person's edited rule list as approved, which
// clears drafted tasks (they were made from the old rules).
func ApproveRules(d *Draft, m *scaffold.MockSpec, rules []Rule, by string) error {
	if d.Status == StatusWritten {
		return errors.New("tasks from this draft are already in the benchmark; start a new draft to change the rules")
	}
	if err := CheckRules(rules, m); err != nil {
		return err
	}
	for i := range rules {
		rules[i].Text = strings.TrimSpace(rules[i].Text)
		if rules[i].Operations == nil {
			rules[i].Operations = []string{}
		}
	}
	d.Rules, d.ApprovedBy, d.Status = rules, by, StatusRulesApproved
	d.Plans, d.Dropped, d.Seed = []*Plan{}, []Dropped{}, nil
	return nil
}

// ---- generation stages ----

// apiSummary is the API as the prompts show it.
func apiSummary(m *scaffold.MockSpec) string {
	type opView struct {
		Name        string         `json:"name"`
		Method      string         `json:"method"`
		Path        string         `json:"path"`
		Kind        string         `json:"kind"`
		Collection  string         `json:"collection,omitempty"`
		PathParams  []string       `json:"path_params,omitempty"`
		QueryParams []string       `json:"query_params,omitempty"`
		Body        map[string]any `json:"body_schema,omitempty"`
		Description string         `json:"description,omitempty"`
	}
	var ops []opView
	for _, op := range m.Operations {
		ops = append(ops, opView{op.Name, op.Method, op.Path, op.Kind, op.Collection, op.PathParams, op.QueryParams, op.BodySchema, op.Description})
	}
	b, _ := json.MarshalIndent(map[string]any{"title": m.Title, "operations": ops}, "", " ")
	s := string(b)
	if len(s) > 60000 {
		s = s[:60000] + "\n... (cut)"
	}
	return s
}

func asJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", " ")
	return string(b)
}

// fence keeps uploaded text inside the data markers.
func fence(s string) string {
	s = strings.ReplaceAll(s, "</data>", "</ data>")
	return "<data>\n" + s + "\n</data>"
}

// DraftRules asks the assist for the policy's rules.
func DraftRules(ctx context.Context, a *Assistant, d *Draft, m *scaffold.MockSpec, policy []byte) error {
	if d.Status == StatusWritten {
		return errors.New("tasks from this draft are already in the benchmark; start a new draft")
	}
	var out struct {
		Rules []Rule `json:"rules"`
	}
	rec, err := a.Run(ctx, OpRules, map[string]string{"api": fence(apiSummary(m)), "policy": fence(string(policy))}, 4000, &out,
		func() error { return CheckRules(out.Rules, m) })
	d.Calls = append(d.Calls, rec)
	d.SpentUSD += rec.USD
	d.Model = &a.Model
	if err != nil {
		return err
	}
	for i := range out.Rules {
		if out.Rules[i].Operations == nil {
			out.Rules[i].Operations = []string{}
		}
	}
	d.Rules, d.ApprovedBy, d.Status = out.Rules, "", StatusRulesDrafted
	d.Plans, d.Dropped, d.Seed = []*Plan{}, []Dropped{}, nil
	return nil
}

// GenerateOptions tune task generation.
type GenerateOptions struct {
	PerRule int  // scenarios asked per rule, 1..MaxPerRule (default 2)
	Judged  bool // the benchmark pins a judge: keep reply rubrics
}

// GenerateTasks drafts the seed, then scenarios rule by rule, and proves
// each one. A rule whose scenarios all fail is reported, not retried
// forever; the budget stops everything.
func GenerateTasks(ctx context.Context, a *Assistant, d *Draft, m *scaffold.MockSpec, policy []byte, o GenerateOptions) error {
	if d.Status != StatusRulesApproved && d.Status != StatusGenerated {
		return errors.New("approve the policy rules first: a person checks the rule list once before tasks are drafted")
	}
	if o.PerRule <= 0 {
		o.PerRule = 2
	}
	if o.PerRule > MaxPerRule {
		o.PerRule = MaxPerRule
	}
	d.Model = &a.Model
	d.Judged = o.Judged
	var seedOut struct {
		Collections map[string][]any `json:"collections"`
	}
	rec, err := a.Run(ctx, OpSeed, map[string]string{"api": fence(apiSummary(m)), "rules": fence(PolicyText(d.Rules)),
		"collections": strings.Join(m.Collections(), ", ")}, 8000, &seedOut,
		func() error {
			if len(seedOut.Collections) == 0 {
				return errors.New("collections is empty")
			}
			return ValidateSeed(State(seedOut.Collections), m)
		})
	d.Calls = append(d.Calls, rec)
	d.SpentUSD += rec.USD
	if err != nil {
		return fmt.Errorf("seed data: %w", err)
	}
	d.Seed = State(seedOut.Collections)
	d.Plans, d.Dropped = []*Plan{}, []Dropped{}
	pr := &Prover{Mock: m, Seed: d.Seed, Rules: d.Rules, Judged: o.Judged, App: d.App}
	for _, rule := range d.Rules {
		if len(d.Plans) >= MaxTasks {
			break
		}
		var out struct {
			Scenarios []Scenario `json:"scenarios"`
		}
		rec, err := a.Run(ctx, OpScenarios, map[string]string{"api": fence(apiSummary(m)), "rules": fence(PolicyText(d.Rules)),
			"rule": fence(asJSON(rule)), "seed": fence(asJSON(d.Seed)), "count": fmt.Sprint(o.PerRule)}, 8000, &out,
			func() error {
				if len(out.Scenarios) == 0 {
					return errors.New("scenarios is empty")
				}
				return nil
			})
		d.Calls = append(d.Calls, rec)
		d.SpentUSD += rec.USD
		if errors.Is(err, ErrBudget) {
			d.Status = StatusGenerated
			return err
		}
		if err != nil {
			d.Dropped = append(d.Dropped, Dropped{RuleID: rule.ID, Reason: "no scenarios drafted: " + err.Error()})
			continue
		}
		for _, sc := range out.Scenarios {
			if sc.RuleID == "" {
				sc.RuleID = rule.ID
			}
			if sc.RuleID != rule.ID {
				d.Dropped = append(d.Dropped, Dropped{RuleID: sc.RuleID, Title: sc.Title, Reason: "drafted for rule " + rule.ID + " but names another rule"})
				continue
			}
			p, err := pr.Prove(sc)
			if err != nil {
				d.Dropped = append(d.Dropped, Dropped{RuleID: sc.RuleID, Title: sc.Title, Reason: err.Error()})
				continue
			}
			d.Plans = append(d.Plans, p)
		}
	}
	d.Status = StatusGenerated
	return nil
}
