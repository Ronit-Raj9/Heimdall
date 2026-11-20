package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// modeAnalyzer is fakeAnalyzer whose analysis records mode.
type modeAnalyzer struct {
	fakeAnalyzer
	mode schemas.Mode
}

func (m *modeAnalyzer) Analyze(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, error) {
	a, err := m.fakeAnalyzer.Analyze(ctx, expID, gradeSource)
	if a != nil {
		a.Mode = m.mode
	}
	return a, err
}

// TestVerdictCarriesAnalysisMode is the regression test for audit finding
// 7-B-03: a live server answering the verdict of a synthetic or recorded
// analysis labels the answer with the analysis's mode, so a client that
// requires live refuses it. The server label still caps the analysis label.
func TestVerdictCarriesAnalysisMode(t *testing.T) {
	tests := []struct {
		server, analysis, want schemas.Mode
	}{
		{schemas.ModeLive, schemas.ModeSynthetic, schemas.ModeSynthetic},
		{schemas.ModeLive, schemas.ModeRecorded, schemas.ModeRecorded},
		{schemas.ModeLive, schemas.ModeLive, schemas.ModeLive},
		{schemas.ModeLive, "", schemas.ModeLive},
		{schemas.ModeRecorded, schemas.ModeLive, schemas.ModeRecorded},
	}
	for _, tt := range tests {
		t.Run(string(tt.server)+"/"+string(tt.analysis), func(t *testing.T) {
			d := fullDeps()
			r := router.New()
			NewExperimentsHandler(tt.server, d.index, &modeAnalyzer{mode: tt.analysis}, d.launcher, d.catalog, nil).RegisterRoutes(r)
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetRequestURI(APIPrefix + "/experiments/e_test/verdict")
			ctx.Request.Header.SetMethod(http.MethodGet)
			r.Handler(ctx)
			if ctx.Response.StatusCode() != http.StatusOK {
				t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
			}
			if got := string(ctx.Response.Header.Peek(lib.ModeHeader)); got != string(tt.want) {
				t.Errorf("header mode %q, want %q", got, tt.want)
			}
			var body struct {
				Mode string `json:"mode"`
			}
			if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Mode != string(tt.want) {
				t.Errorf("body mode %q, want %q", body.Mode, tt.want)
			}
		})
	}
}
