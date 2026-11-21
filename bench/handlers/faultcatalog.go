package handlers

// Lane X22: GET /api/bench/faults lists every fault an evaluation can ask
// for (bench/faults.Catalog), so the evaluate wizard and the Failure tests
// page read one list instead of keeping their own copies.

import (
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// FaultCatalog is the answer of GET /faults.
type FaultCatalog struct {
	Faults []faults.Fault `json:"faults"`
}

func init() {
	RegisterOrgArea("faultcatalog", func(env OrgEnv, r *router.Router) {
		r.GET(APIPrefix+"/faults", env.W(func(ctx *fasthttp.RequestCtx) {
			lib.SendJSON(ctx, env.Mode, fasthttp.StatusOK, FaultCatalog{Faults: faults.Catalog})
		}))
	})
}
