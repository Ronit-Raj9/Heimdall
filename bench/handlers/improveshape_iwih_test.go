package handlers

// The improvement pages (ui/lib/types/improve.ts) were built against the
// contract types before the server lanes landed. These tests read the page's
// interfaces from the TS file and check them against the server: the Go
// types (every field the page names is served under that name, and a field
// the page treats as always present is not dropped by omitempty), and the
// real answers of every route the pages call (every field the page treats
// as always present is in the answer, at every depth).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/valyala/fasthttp"
)

// tsField is one field of a page interface: its name, whether the page
// marks it optional, and its TS type text.
type tsField struct {
	name     string
	optional bool
	typ      string
}

var (
	tsIfaceRe = regexp.MustCompile(`^export interface (\w+)(?: extends Omit<(\w+), "(\w+)">)? \{$`)
	tsFieldRe = regexp.MustCompile(`^(\w+)(\?)?: (.*)$`)
)

// readTSInterfaces parses the fields of each exported interface of the
// page's type file; a field whose type is an inline object spanning lines is
// joined into one type text. An interface that extends Omit<X, "k"> gets
// X's fields except k, then its own.
func readTSInterfaces(t *testing.T) map[string][]tsField {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "ui", "lib", "types", "improve.ts"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]tsField{}
	var cur string
	var pending *tsField
	depth := 0
	for _, line := range strings.Split(string(b), "\n") {
		if m := tsIfaceRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			if m[2] != "" {
				for _, f := range out[m[2]] {
					if f.name != m[3] {
						out[cur] = append(out[cur], f)
					}
				}
			}
			continue
		}
		if cur == "" {
			continue
		}
		if pending != nil {
			s := strings.TrimSpace(line)
			if !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "*") {
				pending.typ += " " + s
				depth += strings.Count(s, "{") - strings.Count(s, "}")
			}
			if depth == 0 {
				out[cur] = append(out[cur], *pending)
				pending = nil
			}
			continue
		}
		if line == "}" {
			cur = ""
			continue
		}
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		m := tsFieldRe.FindStringSubmatch(strings.TrimPrefix(line, "\t"))
		if m == nil {
			continue
		}
		f := tsField{name: m[1], optional: m[2] == "?", typ: m[3]}
		out[cur] = tsDeleteName(out[cur], f.name)
		if d := strings.Count(f.typ, "{") - strings.Count(f.typ, "}"); d > 0 {
			pending, depth = &f, d
			continue
		}
		out[cur] = append(out[cur], f)
	}
	return out
}

func tsDeleteName(fs []tsField, name string) []tsField {
	out := fs[:0]
	for _, f := range fs {
		if f.name != name {
			out = append(out, f)
		}
	}
	return out
}

// tsInline parses the fields of an inline object type "{ a: x; b?: y }".
func tsInline(typ string) []tsField {
	s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(typ), ";"))
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}"))
	var out []tsField
	depth, start := 0, 0
	flush := func(end int) {
		part := strings.TrimSpace(s[start:end])
		if m := tsFieldRe.FindStringSubmatch(part); m != nil {
			out = append(out, tsField{name: m[1], optional: m[2] == "?", typ: m[3]})
		}
	}
	for i, c := range s {
		switch c {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ';', ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(s))
	return out
}

// checkTSShape reports every field the page reads as always present that a
// served JSON value lacks, recursing into named interfaces, arrays of them
// and inline objects.
func checkTSShape(t *testing.T, ifaces map[string][]tsField, fields []tsField, v any, path string) {
	t.Helper()
	obj, ok := v.(map[string]any)
	if !ok {
		t.Errorf("%s: the page expects an object, the server sent %T", path, v)
		return
	}
	for _, f := range fields {
		val, present := obj[f.name]
		nullable := strings.Contains(f.typ, "| null")
		switch {
		case !present && !f.optional:
			t.Errorf("%s.%s: the page reads it as always present, the server did not send it", path, f.name)
			continue
		case !present || val == nil:
			if present && !f.optional && !nullable {
				t.Errorf("%s.%s: the server sent null", path, f.name)
			}
			continue
		}
		typ := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.Split(f.typ, "| null")[0]), ";"))
		switch {
		case strings.HasPrefix(typ, "{"):
			checkTSShape(t, ifaces, tsInline(typ), val, path+"."+f.name)
		case strings.HasSuffix(typ, "[]"):
			if sub, ok := ifaces[strings.TrimSuffix(typ, "[]")]; ok {
				arr, isArr := val.([]any)
				if !isArr {
					t.Errorf("%s.%s: the page expects an array", path, f.name)
					continue
				}
				for i, x := range arr {
					checkTSShape(t, ifaces, sub, x, fmt.Sprintf("%s.%s[%d]", path, f.name, i))
				}
			}
		default:
			if sub, ok := ifaces[typ]; ok {
				checkTSShape(t, ifaces, sub, val, path+"."+f.name)
			}
		}
	}
}

// pageShape checks one answer body against a page interface.
func pageShape(t *testing.T, ifaces map[string][]tsField, iface string, body []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%s: %v: %s", iface, err, body)
	}
	fields, ok := ifaces[iface]
	if !ok {
		t.Fatalf("no page interface %s", iface)
	}
	checkTSShape(t, ifaces, fields, v, iface)
}

// goJSONKeys are a struct's JSON keys (embedded structs flattened) and
// whether each may be left out.
func goJSONKeys(t reflect.Type) map[string]bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	out := map[string]bool{}
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if f.Anonymous && tag == "" {
			for k, v := range goJSONKeys(f.Type) {
				out[k] = v
			}
			continue
		}
		if !f.IsExported() || tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out[name] = strings.Contains(opts, "omitempty")
	}
	return out
}

// improvePageTypes maps each page interface to the Go type the server
// answers with.
var improvePageTypes = map[string]reflect.Type{
	"ImprovementSummary":  reflect.TypeOf(improve.Summary{}),
	"ImprovementList":     reflect.TypeOf(improve.List{}),
	"ImprovementDetail":   reflect.TypeOf(improve.Detail{}),
	"CurvePoint":          reflect.TypeOf(improve.CurvePoint{}),
	"ControlBand":         reflect.TypeOf(improve.ControlBand{}),
	"ScoreView":           reflect.TypeOf(improve.ScoreView{}),
	"EValueTest":          reflect.TypeOf(stats.EValueTest{}),
	"CompareView":         reflect.TypeOf(improve.CompareView{}),
	"DiffLine":            reflect.TypeOf(improve.DiffLine{}),
	"FileDiff":            reflect.TypeOf(improve.FileDiff{}),
	"GateResult":          reflect.TypeOf(improve.GateResult{}),
	"Approval":            reflect.TypeOf(improve.Approval{}),
	"RoundCost":           reflect.TypeOf(improve.RoundCost{}),
	"RoundView":           reflect.TypeOf(improve.RoundView{}),
	"LineageNode":         reflect.TypeOf(improve.Node{}),
	"LineageView":         reflect.TypeOf(improve.LineageView{}),
	"IntegrityRow":        reflect.TypeOf(improve.IntegrityRow{}),
	"CostView":            reflect.TypeOf(improve.CostView{}),
	"ImprovementVerdict":  reflect.TypeOf(improve.Verdict{}),
	"ControlLine":         reflect.TypeOf(improve.ControlLine{}),
	"ContinueOffer":       reflect.TypeOf(improve.ContinueOffer{}),
	"ContinueRequest":     reflect.TypeOf(improve.ContinueRequest{}),
	"EstimateNote":        reflect.TypeOf(improve.EstimateNote{}),
	"ImprovementEstimate": reflect.TypeOf(improve.Estimate{}),
	"AppliedEditView":     reflect.TypeOf(improve.EditView{}),
	"CandidateView":       reflect.TypeOf(improve.CandidateView{}),
	"CommitView":          reflect.TypeOf(improve.CommitView{}),
	"StageView":           reflect.TypeOf(improve.StageResult{}),
	"KeepView":            reflect.TypeOf(improve.KeepView{}),
	"FinalView":           reflect.TypeOf(improve.FinalView{}),
	"ExplainEdit":         reflect.TypeOf(improve.ExplainEdit{}),
	"ExplainFile":         reflect.TypeOf(improve.ExplainFile{}),
	"VersionExplain":      reflect.TypeOf(improve.Explain{}),
}

// Item 7 of the group H merge: every interface of the page's type file has
// a server type, every field it names is served under that name, and a
// field the page reads as always present is never left out.
func TestImprovePageTypesMatchTheServer(t *testing.T) {
	ifaces := readTSInterfaces(t)
	if len(ifaces) < 30 {
		t.Fatalf("parsed %d interfaces from improve.ts", len(ifaces))
	}
	names := make([]string, 0, len(ifaces))
	for n := range ifaces {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		gt, ok := improvePageTypes[n]
		if !ok {
			t.Errorf("page type %s has no server type in this test", n)
			continue
		}
		keys := goJSONKeys(gt)
		for _, f := range ifaces[n] {
			omit, served := keys[f.name]
			switch {
			case !served:
				t.Errorf("%s.%s: the server type %s has no field %q", n, f.name, gt.Name(), f.name)
			case omit && !f.optional:
				t.Errorf("%s.%s: the page reads it as always present, the server leaves it out when empty (omitempty)", n, f.name)
			}
		}
	}
	// The inline spec of the detail and the create body name Spec fields.
	spec := goJSONKeys(reflect.TypeOf(improve.Spec{}))
	for _, k := range []string{"kind", "name", "agent", "suite", "rounds", "preset", "editable", "improver", "budget", "commit", "sealed"} {
		if _, ok := spec[k]; !ok {
			t.Errorf("improve.Spec has no field %q the page sends or reads", k)
		}
	}
}

// explainingSource is the server's source with the runner's explain.
type explainingSource struct{ extendSource }

func (s explainingSource) Explain(_ context.Context, id string, n int) (*improve.Explain, error) {
	return s.r.Explain(id, n)
}

// webFormBody is the create body the page's form builds (improvementSpec in
// newImprovementDialog.tsx) for a named preset.
const webFormBody = `{"kind":"improvement","name":"web-run","agent":"agents/scripted-improver.yaml","suite":"scripted@1.0.0","preset":"small",` +
	`"editable":{"instructions":true,"files":["/home/agent/AGENTS.md"]},"improver":{"task":"heimdall-improver@1.1.0:reflect-edit"},"budget":{"total_usd":40}}`

// Item 7: every route the improvement pages call answers, under the path
// and method the page uses, a body with every field the page reads as
// always present. The create body is the web form's exactly; the improver
// task it sends is the server's default task.
func TestImprovePageRoutesAnswerThePageShape(t *testing.T) {
	ifaces := readTSInterfaces(t)
	b, err := os.ReadFile(filepath.Join("..", "ui", "components", "improvements", "newImprovementDialog.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `export const DEFAULT_IMPROVER_TASK = "`+improve.DefaultImproverTask+`";`) {
		t.Fatalf("the web form's improver task is not the server's default %s", improve.DefaultImproverTask)
	}
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 60}
	root := filepath.Join(t.TempDir(), improve.DefaultDir)
	r := &improve.Runner{Root: root, Exec: x, Objects: improve.Objects{Root: filepath.Join(root, "objects")}}
	src := explainingSource{extendSource{runnerSource{r: r}}}
	c := newImproveClient(t, src)

	// Create (POST /improvements) with the form's body.
	resp := c.do(t, "POST", APIPrefix+"/improvements", []byte(webFormBody), authed("application/json"))
	if resp.StatusCode() != 202 {
		t.Fatalf("create: %d %s", resp.StatusCode(), resp.Body())
	}
	pageShape(t, ifaces, "ImprovementSummary", resp.Body())
	var sum improve.Summary
	_ = json.Unmarshal(resp.Body(), &sum)
	rec, err := r.Get(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Spec.Improver.Task != improve.DefaultImproverTask || !rec.Spec.Editable.Instructions || rec.Spec.Budget.TotalUSD != 40 {
		t.Errorf("stored spec: improver %+v editable %+v budget %+v", rec.Spec.Improver, rec.Spec.Editable, rec.Spec.Budget)
	}

	// List and detail.
	resp = c.do(t, "GET", APIPrefix+"/improvements", nil, nil)
	pageShape(t, ifaces, "ImprovementList", resp.Body())
	resp = c.do(t, "GET", APIPrefix+"/improvements/"+sum.ID, nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("detail: %d %s", resp.StatusCode(), resp.Body())
	}
	pageShape(t, ifaces, "ImprovementDetail", resp.Body())
	var d improve.Detail
	_ = json.Unmarshal(resp.Body(), &d)
	if len(d.Rounds) == 0 || d.Verdict == nil {
		t.Fatalf("detail has no rounds or verdict: %s", resp.Body())
	}

	// Explain a kept version (GET .../versions/{n}/explain).
	er := router.New()
	old := OrgImproveSource
	t.Cleanup(func() { OrgImproveSource = old })
	OrgImproveSource = func(OrgEnv) ImproveSource { return src }
	registerImproveExplain(OrgEnv{}, er)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI(fmt.Sprintf("%s/improvements/%s/versions/%d/explain", APIPrefix, sum.ID, rec.Incumbent.Number))
	er.Handler(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("explain: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	pageShape(t, ifaces, "VersionExplain", ctx.Response.Body())

	// Continue estimate and continue on a run that may continue (the
	// detail says whether it may).
	body := []byte(`{"extra_trials":2,"new_tasks":true}`)
	resp = c.do(t, "POST", APIPrefix+"/improvements/"+sum.ID+"/continue/estimate", body, nil)
	if d.CanContinue {
		if resp.StatusCode() != 200 {
			t.Fatalf("continue estimate: %d %s", resp.StatusCode(), resp.Body())
		}
		pageShape(t, ifaces, "ContinueOffer", resp.Body())
		resp = c.do(t, "POST", APIPrefix+"/improvements/"+sum.ID+"/continue", body, authed(""))
		if resp.StatusCode() != 202 {
			t.Fatalf("continue: %d %s", resp.StatusCode(), resp.Body())
		}
		pageShape(t, ifaces, "ImprovementSummary", resp.Body())
	} else if resp.StatusCode() != 409 && resp.StatusCode() != 200 {
		t.Errorf("continue estimate on a run that may not continue: %d %s", resp.StatusCode(), resp.Body())
	}

	// Approve (POST .../rounds/{k}/approve), stop and resume on a run that
	// pauses at a tripwire.
	cx := &improvetest.Exec{Behavior: improvetest.Cheat, Base: 1, NTasks: 60}
	cr := &improve.Runner{Root: filepath.Join(t.TempDir(), improve.DefaultDir), Exec: cx}
	cc := newImproveClient(t, extendSource{runnerSource{r: cr}})
	resp = cc.do(t, "POST", APIPrefix+"/improvements", []byte(strings.Replace(webFormBody, `"web-run"`, `"web-pause"`, 1)), authed("application/json"))
	_ = json.Unmarshal(resp.Body(), &sum)
	if sum.Status != improve.StatusAwaiting {
		t.Fatalf("paused run: %s %s", sum.Status, resp.Body())
	}
	resp = cc.do(t, "POST", fmt.Sprintf("%s/improvements/%s/rounds/1/approve", APIPrefix, sum.ID), []byte(`{"reason":"checked by hand"}`), authed("application/json"))
	if resp.StatusCode() != 202 {
		t.Fatalf("approve: %d %s", resp.StatusCode(), resp.Body())
	}
	pageShape(t, ifaces, "ImprovementSummary", resp.Body())
	resp = cc.do(t, "POST", APIPrefix+"/improvements", []byte(strings.Replace(webFormBody, `"web-run"`, `"web-stop"`, 1)), authed("application/json"))
	_ = json.Unmarshal(resp.Body(), &sum)
	resp = cc.do(t, "POST", APIPrefix+"/improvements/"+sum.ID+"/stop", nil, authed(""))
	if resp.StatusCode() != 200 && resp.StatusCode() != 202 {
		t.Fatalf("stop: %d %s", resp.StatusCode(), resp.Body())
	}
	pageShape(t, ifaces, "ImprovementSummary", resp.Body())
	resp = cc.do(t, "POST", APIPrefix+"/improvements/"+sum.ID+"/resume", nil, authed(""))
	if resp.StatusCode() != 409 {
		t.Errorf("resume of a stopped run: %d %s", resp.StatusCode(), resp.Body())
	}
	writeImproveRecord(t, cr.Root, &improve.Record{ID: "imp_web-int", Name: "web-int", Status: improve.StatusInterrupted, Rounds: []*improve.Round{}})
	resp = cc.do(t, "POST", APIPrefix+"/improvements/imp_web-int/resume", nil, authed(""))
	if resp.StatusCode() != 202 {
		t.Fatalf("resume: %d %s", resp.StatusCode(), resp.Body())
	}
	pageShape(t, ifaces, "ImprovementSummary", resp.Body())
}

// Item 7: the estimate route answers the page's estimate shape, nested
// parts included.
func TestImproveEstimateAnswersThePageShape(t *testing.T) {
	ifaces := readTSInterfaces(t)
	old := ImproveEstimateInputs
	t.Cleanup(func() { ImproveEstimateInputs = old })
	ImproveEstimateInputs = func(context.Context, *improve.Spec) (improve.EstimateInput, error) {
		return improve.EstimateInput{Pool: 60, Splits: improve.SplitCounts{Practice: 24, Gate: 24, HeldOut: 12}, PerRunUSD: 0.02, RunSource: "forecast", PerRunMin: 3}, nil
	}
	ctx := postImproveEstimate(t, webFormBody)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("estimate: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	pageShape(t, ifaces, "ImprovementEstimate", ctx.Response.Body())
}
