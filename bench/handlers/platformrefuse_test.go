package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type hw05Suites struct{ suites map[string]*schemas.Suite }

func (f hw05Suites) SuiteIDs(context.Context) ([]string, error) { return nil, nil }
func (f hw05Suites) LoadSuite(_ context.Context, id string) (*schemas.Suite, error) {
	if s, ok := f.suites[id]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("suite %s: %w", id, schemas.ErrNotFound)
}
func (f hw05Suites) LockDrift(context.Context, *schemas.Suite) ([]schemas.SuiteDrift, error) {
	return nil, nil
}

func (f hw05Suites) FingerprintDrift(context.Context, *schemas.Suite) ([]schemas.FingerprintDrift, error) {
	return nil, nil
}

type hw05Agents struct{}

func (hw05Agents) AgentConfigs(context.Context) ([]AgentConfigFile, error) {
	return []AgentConfigFile{{Path: "agents/codex.yaml", Config: &schemas.AgentConfig{ID: "codex", Adapter: "codex"}}}, nil
}

// hw05Handler is an evaluations handler of org_a over a BFCL-shaped
// benchmark (snapshot graded, each task its own image) and a
// coding-core-shaped one.
func hw05Handler() *evaluationsHandler {
	suites := hw05Suites{suites: map[string]*schemas.Suite{
		"hd-harbor-bfcl": {ID: "hd-harbor-bfcl", Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot},
			Tasks: []*schemas.Task{{ID: "t01", Environment: &schemas.TaskEnvironment{Image: "bench/task-bfcl-t01:1"}}}},
		"coding-core": {ID: "coding-core", Tasks: []*schemas.Task{{ID: "t01"}, {ID: "t02"}}},
	}}
	return &evaluationsHandler{env: OrgEnv{Services: Services{OrgID: "org_a", Hosted: true, Suites: suites, Agents: hw05Agents{}}}}
}

func hw05Spec(benches ...string) *schemas.EvaluationSpec {
	return &schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "gpt-5.4-mini"}},
		Benchmarks: benches, TasksPerBenchmark: 10, Trials: 1}
}

// withPlatformTarget installs a resolver that sends the shared queue to
// modal, with imagesReady as given, for the test.
func withPlatformTarget(t *testing.T, imagesReady bool) {
	t.Helper()
	old := platformTargetResolver
	SetPlatformTargetResolver(func(_ context.Context, org, worker string) (string, bool, error) {
		if org != "org_a" || worker != "" {
			return "", false, nil
		}
		return "modal", imagesReady, nil
	})
	t.Cleanup(func() { platformTargetResolver = old })
}

type hw05Body struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
	Benchmarks []struct {
		ID      string   `json:"id"`
		Reasons []string `json:"reasons"`
	} `json:"benchmarks"`
}

func hw05Decode(t *testing.T, body any) hw05Body {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var out hw05Body
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPlatformRefusalRefusesBenchmark covers N216 fix 3: an evaluation that
// goes to Modal is refused before anything is saved, 422 with each
// benchmark Modal cannot run and its plain reasons; a benchmark Modal can
// run is not listed, and an evaluation of only such benchmarks passes.
func TestPlatformRefusalRefusesBenchmark(t *testing.T) {
	withPlatformTarget(t, true)
	h := hw05Handler()
	status, body, refused := platformRefusal(context.Background(), h, hw05Spec("coding-core", "hd-harbor-bfcl"))
	if !refused || status != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("refused %v status %d", refused, status)
	}
	b := hw05Decode(t, body)
	if b.Error.Type != "not_on_platform" || len(b.Benchmarks) != 1 || b.Benchmarks[0].ID != "hd-harbor-bfcl" {
		t.Fatalf("body %+v", b)
	}
	want := "Modal cannot run hd-harbor-bfcl: it is graded from a copy of the whole machine, which a Modal sandbox cannot make. Run it on the shared worker or your own worker."
	if b.Error.Message != want {
		t.Fatalf("message %q, want %q", b.Error.Message, want)
	}
	if got := strings.Join(b.Benchmarks[0].Reasons, " | "); !strings.Contains(got, "its tasks bring their own machine image") {
		t.Fatalf("reasons %q", got)
	}
	if _, _, refused := platformRefusal(context.Background(), h, hw05Spec("coding-core")); refused {
		t.Fatal("coding-core refused on Modal")
	}
	SetPlatformTargetResolver(func(context.Context, string, string) (string, bool, error) { return "", false, nil })
	if _, _, refused := platformRefusal(context.Background(), h, hw05Spec("hd-harbor-bfcl")); refused {
		t.Fatal("refused although the runs go to the shared worker")
	}
}

// TestPlatformRefusalWithoutRegistry: while the platform has no image
// registry (platformImagesReady false), every benchmark is refused with the
// registry sentence.
func TestPlatformRefusalWithoutRegistry(t *testing.T) {
	withPlatformTarget(t, false)
	status, body, refused := platformRefusal(context.Background(), hw05Handler(), hw05Spec("coding-core", "hd-harbor-bfcl"))
	if !refused || status != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("refused %v status %d", refused, status)
	}
	b := hw05Decode(t, body)
	const sentence = "Modal needs an image registry it can pull from. Add one on the Modal card."
	if b.Error.Message != sentence || len(b.Benchmarks) != 2 {
		t.Fatalf("body %+v", b)
	}
	for _, x := range b.Benchmarks {
		if len(x.Reasons) != 1 || x.Reasons[0] != sentence {
			t.Errorf("benchmark %s reasons %q", x.ID, x.Reasons)
		}
	}
}
