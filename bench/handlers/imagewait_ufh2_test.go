package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A suite whose tasks bring their own image carries the image wait on the
// estimate (and in its JSON answer); a suite without task images does not.
func TestEstimateCarriesTaskImageWait(t *testing.T) {
	plain := taskImageSuite()
	plain.ID = "plain"
	plain.Verifier.Mode = ""
	for _, tk := range plain.Tasks {
		tk.Environment = nil
	}
	suites := &fakeSuites{ids: []string{"hd-harbor-tb2", "plain"}, suites: map[string]*schemas.Suite{"hd-harbor-tb2": taskImageSuite(), "plain": plain}}
	agents := map[string]*schemas.AgentConfig{"agents/codex.yaml": codexLike()}
	src := &pricedForecast{priced: map[string]bool{"gpt-x": true}}

	req := &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"hd-harbor-tb2"}, TasksPerBenchmark: 2, Trials: 1}
	est, err := EstimateEvaluationWith(context.Background(), suites, src, req, agents, "")
	if err != nil {
		t.Fatal(err)
	}
	w := est.ImageWait
	if w == nil || w.Images != 2 || !w.UpTo || w.Minutes != 2 || w.TotalMinutes != 3 { // one image before the first run; 2 x 75 s in all
		t.Fatalf("image wait %+v", w)
	}
	data, _ := json.Marshal(est)
	var shape struct {
		ImageWait *struct {
			Images  int  `json:"images"`
			Minutes int  `json:"minutes"`
			UpTo    bool `json:"up_to"`
		} `json:"image_wait"`
	}
	if json.Unmarshal(data, &shape) != nil || shape.ImageWait == nil || shape.ImageWait.Images != 2 {
		t.Fatalf("json %s", data)
	}

	req = &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"plain"}, TasksPerBenchmark: 2, Trials: 1}
	est, err = EstimateEvaluationWith(context.Background(), suites, src, req, agents, "")
	if err != nil {
		t.Fatal(err)
	}
	if est.ImageWait != nil {
		t.Fatalf("plain suite has an image wait %+v", est.ImageWait)
	}
	shape.ImageWait = nil
	if data, _ := json.Marshal(est); json.Unmarshal(data, &shape) != nil || shape.ImageWait != nil {
		t.Fatalf("json %s", data)
	}
}
