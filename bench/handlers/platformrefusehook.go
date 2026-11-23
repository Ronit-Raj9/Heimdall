package handlers

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// platformRefusal checks, before anything is saved, an evaluation whose runs
// go to a sandbox platform (fix wave 3, N216). Lane H05 sets it in the init()
// of platformrefuse.go; nil refuses nothing. When refused is true, create
// answers status with body and saves nothing.
var platformRefusal func(ctx context.Context, h *evaluationsHandler, spec *schemas.EvaluationSpec) (status int, body any, refused bool)
