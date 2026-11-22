package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/connect"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

var connectCodeRe = regexp.MustCompile(`HMD-[A-Z0-9]{5}`)

func connectRouter(t *testing.T, suites SuiteSource) (*router.Router, projectfs.Store) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Mode: schemas.Mode("live"), Services: Services{Projects: st, Suites: suites}}
	h := &connectHandler{env: env, detect: connect.Detect, verify: connect.Verify}
	r := router.New()
	p := APIPrefix + "/connect"
	r.GET(p+"/failures", h.catalog)
	r.POST(p+"/detect", h.detectRoute)
	r.POST(p+"/agents", h.save)
	r.POST(p+"/verify", h.startVerify)
	r.POST(p+"/first-look", h.firstLook)
	r.POST(p+"/explain", h.explain)
	return r, st
}

// a FastAPI-style agent: wants "query", answers {"reply": {"text": ...}},
// remembers by session_id.
func queryAgent() *httptest.Server {
	mem := map[string]string{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		q, _ := body["query"].(string)
		if q == "" {
			w.WriteHeader(422)
			fmt.Fprint(w, `{"detail":[{"loc":["body","query"],"msg":"Field required"}]}`)
			return
		}
		sess, _ := body["session_id"].(string)
		reply := connectCodeRe.FindString(q)
		switch {
		case strings.HasPrefix(q, "Remember"):
			mem[sess], reply = reply, "OK"
		case strings.HasPrefix(q, "What code"):
			reply = mem[sess]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"reply": map[string]any{"text": reply}})
	}))
}

func TestConnectDetectSaveVerify(t *testing.T) {
	srv := queryAgent()
	defer srv.Close()
	r, st := connectRouter(t, nil)
	code, body := abCall(r, "POST", APIPrefix+"/connect/detect", ConnectDetectRequest{Input: srv.URL + "/ask"})
	if code != 200 {
		t.Fatalf("detect %d %s", code, body)
	}
	var det ConnectDetectResponse
	if err := json.Unmarshal(body, &det); err != nil {
		t.Fatal(err)
	}
	best := det.Best()
	if best == nil || !best.NonceFound || best.Mapping == nil || best.Mapping.AnswerPath != "reply.text" {
		t.Fatalf("best %+v", best)
	}
	if det.Preview == nil || det.Preview.Error != "" || !strings.Contains(det.Preview.YAML, "answer_path: reply.text") {
		t.Fatalf("preview %+v", det.Preview)
	}
	// the session field is sent so the agent can remember
	best.Mapping.Body = `{"query": "{{instruction}}", "session_id": "{{session_id}}"}`
	code, body = abCall(r, "POST", APIPrefix+"/connect/agents", ConnectSaveRequest{ID: "ask-bot", Candidate: *best})
	if code != 201 {
		t.Fatalf("save %d %s", code, body)
	}
	doc, _, err := st.Read(context.Background(), projectfs.AreaAgents, "ask-bot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeAgent(doc, "agents/ask-bot.yaml")
	if err != nil || cfg.Remote == nil || cfg.Remote.Mapping == nil || cfg.Remote.Mapping.AnswerPath != "reply.text" {
		t.Fatalf("saved config %+v %v\n%s", cfg, err, doc)
	}
	code, _ = abCall(r, "POST", APIPrefix+"/connect/agents", ConnectSaveRequest{ID: "ask-bot", Candidate: *best})
	if code != 409 {
		t.Fatalf("second save %d, want 409", code)
	}
	// no job runner here: the rows run in the handler
	code, body = abCall(r, "POST", APIPrefix+"/connect/verify", ConnectVerifyRequest{Agent: "ask-bot", SkipRealTask: true})
	if code != 200 {
		t.Fatalf("verify %d %s", code, body)
	}
	var vs ConnectVerifyStatus
	_ = json.Unmarshal(body, &vs)
	if vs.Result == nil || !vs.Result.OK || vs.Result.Suggest.History != "server" {
		t.Fatalf("verify %+v", vs)
	}
}

func TestConnectSaveRefusesSecretValues(t *testing.T) {
	r, _ := connectRouter(t, nil)
	c := connect.Candidate{Template: connect.TemplateHTTP, Label: "x", Params: map[string]string{"url": "https://bot.example.com/chat", "auth_secret": "sk_live_secret"},
		Questions: []connect.Question{{Kind: "secret", Param: "auth_secret", Secret: "BOT_KEY", Prefill: "sk_live_secret"}}}
	code, body := abCall(r, "POST", APIPrefix+"/connect/agents", ConnectSaveRequest{ID: "bot", Candidate: c, DryRun: true})
	if code != 400 || !strings.Contains(string(body), "secret value") {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestConnectFirstLookAndCatalog(t *testing.T) {
	old := evalCompat
	evalCompat = func(a *schemas.AgentConfig, s *schemas.Suite) (bool, []schemas.Capability, string) {
		return s.ID != "hd-swe-verified", nil, ""
	}
	defer func() { evalCompat = old }()
	tasks := []*schemas.Task{{ID: "t1", Timeouts: schemas.TaskTimeouts{AgentS: 120}}, {ID: "t2", Timeouts: schemas.TaskTimeouts{AgentS: 120}}}
	fast := []*schemas.Task{{ID: "a", Timeouts: schemas.TaskTimeouts{AgentS: 30}}}
	suites := &fakeSuites{ids: []string{"hd-swe-verified", "slowqa", "fastqa"}, suites: map[string]*schemas.Suite{
		"hd-swe-verified": {ID: "hd-swe-verified", TaskIDs: []string{"x"}},
		"slowqa":          {ID: "slowqa", TaskIDs: []string{"t1", "t2"}, Tasks: tasks},
		"fastqa":          {ID: "fastqa", TaskIDs: []string{"a"}, Tasks: fast},
	}}
	r, st := connectRouter(t, suites)
	_ = st
	c := connect.Candidate{Template: connect.TemplateHTTP, Label: "bot", Params: map[string]string{"url": "https://bot.example.com/run"}}
	if code, body := abCall(r, "POST", APIPrefix+"/connect/agents", ConnectSaveRequest{ID: "bot", Candidate: c}); code != 201 {
		t.Fatalf("save %d %s", code, body)
	}
	code, body := abCall(r, "POST", APIPrefix+"/connect/first-look", ConnectFirstLookRequest{Agent: "bot"})
	if code != 200 {
		t.Fatalf("first look %d %s", code, body)
	}
	var fl ConnectFirstLookResponse
	_ = json.Unmarshal(body, &fl)
	if fl.Pick.Benchmark != "fastqa" || fl.Evaluation.Preset != "first_look" || fl.Evaluation.Benchmarks[0] != "fastqa" || fl.Evaluation.TasksPerBenchmark != 1 {
		t.Fatalf("pick %+v", fl)
	}
	// H06: "Compare with the bare model" is a second card; the first look keeps its preset.
	if fl.BareModel == nil || fl.BareModel.Arms[1].Model != fl.Evaluation.Arms[0].Model {
		t.Fatalf("bare-model card on the agent's model: %+v %q", fl.BareModel, fl.BareModelReason)
	}
	code, body = abCall(r, "POST", APIPrefix+"/connect/first-look", ConnectFirstLookRequest{Agent: "bot", Model: "gpt-x", Provider: "openai"})
	fl = ConnectFirstLookResponse{}
	_ = json.Unmarshal(body, &fl)
	bm := fl.BareModel
	if code != 200 || fl.Evaluation.Preset != "first_look" || fl.Evaluation.Mode != "score" || len(fl.Evaluation.Arms) != 1 || bm == nil {
		t.Fatalf("first look with model %d %s", code, body)
	}
	if bm.Mode != "opponent" || bm.Preset != "" || len(bm.Arms) != 2 || bm.Arms[1].Agent != "bare-model" || bm.Arms[1].Model != "gpt-x" ||
		bm.Benchmarks[0] != "fastqa" || bm.TasksPerBenchmark != 1 || bm.Trials != 1 || bm.Concurrency != 5 {
		t.Fatalf("bare-model card %+v", bm)
	}
	evalCompat = func(a *schemas.AgentConfig, s *schemas.Suite) (bool, []schemas.Capability, string) {
		if utils.IsBareModel(a) {
			return false, nil, "it grades a code change"
		}
		return s.ID != "hd-swe-verified", nil, ""
	}
	code, body = abCall(r, "POST", APIPrefix+"/connect/first-look", ConnectFirstLookRequest{Agent: "bot", Model: "gpt-x"})
	fl = ConnectFirstLookResponse{}
	_ = json.Unmarshal(body, &fl)
	if code != 200 || fl.BareModel != nil || !strings.Contains(fl.BareModelReason, "code change") {
		t.Fatalf("incompatible bare model %d %s", code, body)
	}
	code, body = abCall(r, "GET", APIPrefix+"/connect/failures", nil)
	var cat ConnectCatalog
	_ = json.Unmarshal(body, &cat)
	if code != 200 || len(cat.Failures) != 24 || len(cat.Platforms) == 0 {
		t.Fatalf("catalog %d %d", code, len(cat.Failures))
	}
	code, body = abCall(r, "POST", APIPrefix+"/connect/explain", ConnectExplainRequest{Text: "the agent answered HTTP 429"})
	if code != 200 || !strings.Contains(string(body), `"rate_limit"`) {
		t.Fatalf("explain %d %s", code, body)
	}
}

func TestFirstLookPreset(t *testing.T) {
	spec := schemas.EvaluationSpec{Mode: schemas.EvaluationModeScore, Preset: "first_look", Benchmarks: []string{"tau2"}, Trials: 3, TasksPerBenchmark: 20, Faults: []string{"model-down"}}
	if err := normalizeFirstLook(&spec); err != nil {
		t.Fatal(err)
	}
	if spec.TasksPerBenchmark != 5 || spec.Trials != 1 || len(spec.Faults) != 0 || evaluationConcurrency(spec) != 5 {
		t.Fatalf("spec %+v", spec)
	}
	if evaluationConcurrency(schemas.EvaluationSpec{}) != EvaluationDefaultConcurrency {
		t.Fatal("default concurrency changed")
	}
	bad := []schemas.EvaluationSpec{
		{Mode: schemas.EvaluationModeScore, Preset: "first_look", Benchmarks: []string{"a", "b"}},
		{Mode: schemas.EvaluationModeScore, Preset: "first_look", Pack: "support"},
		{Mode: schemas.EvaluationModeModels, Preset: "first_look", Benchmarks: []string{"a"}},
		{Mode: schemas.EvaluationModeScore, Preset: "first_look", Benchmarks: []string{"a"}, Gate: &schemas.EvaluationGate{}},
		{Mode: schemas.EvaluationModeScore, Preset: "huge", Benchmarks: []string{"a"}},
	}
	for i, s := range bad {
		err := normalizeFirstLook(&s)
		if err == nil {
			err = normalizePreset(&s) // an unknown preset is refused there (lane Y10)
		}
		if err == nil {
			t.Errorf("case %d: want an error", i)
		}
	}
}

// B8.task: the verify job and the first look pick a real benchmark over the
// practice examples (acme-*, coding-core); practice is only the fallback,
// and a hosted org never gets the hidden practice suite.
func TestConnectFirstLookSkipsPractice(t *testing.T) {
	old := evalCompat
	evalCompat = func(a *schemas.AgentConfig, s *schemas.Suite) (bool, []schemas.Capability, string) {
		return true, nil, ""
	}
	defer func() { evalCompat = old }()
	fast := []*schemas.Task{{ID: "a", Timeouts: schemas.TaskTimeouts{AgentS: 10}}}
	slow := []*schemas.Task{{ID: "b", Timeouts: schemas.TaskTimeouts{AgentS: 300}}}
	suites := &fakeSuites{ids: []string{"acme-memory", "coding-core", "realqa"}, suites: map[string]*schemas.Suite{
		"acme-memory": {ID: "acme-memory", TaskIDs: []string{"a"}, Tasks: fast},
		"coding-core": {ID: "coding-core", TaskIDs: []string{"a"}, Tasks: fast},
		"realqa":      {ID: "realqa", TaskIDs: []string{"b"}, Tasks: slow},
	}}
	h := &connectHandler{env: OrgEnv{Mode: schemas.Mode("live"), Services: Services{Suites: suites}}}
	cfg := &schemas.AgentConfig{ID: "bot"}
	p, err := h.pick(context.Background(), cfg)
	if err != nil || p.Benchmark != "realqa" || p.Practice {
		t.Fatalf("real benchmark first: %+v %v", p, err)
	}
	suites.ids = []string{"acme-memory", "coding-core"}
	p, err = h.pick(context.Background(), cfg)
	if err != nil || !p.Practice || (p.Benchmark != "acme-memory" && p.Benchmark != "coding-core") {
		t.Fatalf("practice fallback: %+v %v", p, err)
	}
	t.Setenv(benchcatalog.DevEnv, "")
	h.env.Services.Hosted = true
	suites.ids = []string{"coding-core"}
	for _, b := range h.benchmarkInfos(context.Background(), cfg) {
		if b.ID == "coding-core" {
			t.Fatalf("hosted org sees the hidden practice suite: %+v", b)
		}
	}
}
