package handlers

import (
	"github.com/maximhq/bifrost/bench/redteam"
	"github.com/valyala/fasthttp"
)

// Red team (Z04): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/redteam/catalog", Tag: "redteam",
			Summary: "Attack categories, strategies (PAIR, tree of attacks, Crescendo) and channels a red-team benchmark can cover", Response: RedTeamCatalog{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/redteam/benchmark", Tag: "redteam",
			Summary: "Write an adaptive red-team benchmark for your agent; the attacker model runs on your own key",
			Auth:    AuthWrite, Request: RedTeamBenchmarkRequest{}, Status: fasthttp.StatusCreated, Response: RedTeamBenchmarkResult{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/redteam/regressions", Tag: "redteam",
			Summary: "Regression tasks made from successful attacks, with their review state", Response: RedTeamRegressions{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/redteam/regressions/harvest", Tag: "redteam",
			Summary: "Turn every successful attack of an evaluation into a pending regression task (finished evaluations do this on their own)",
			Auth:    AuthWrite, Request: RedTeamHarvestRequest{}, Response: redteam.HarvestResult{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/redteam/regressions/{task}/review", Tag: "redteam",
			Summary: "Accept a regression (it joins the regressions benchmark) or reject it",
			Auth:    AuthWrite, Request: RedTeamReviewRequest{}, Response: redteam.ReviewItem{}},
	)
}
