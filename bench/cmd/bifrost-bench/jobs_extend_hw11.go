package main

import (
	"cmp"
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// extendParentRefusal refuses a follow-up of a comparison whose test was
// broken: its follow-up would inherit the same setups and be broken the
// same way, so "Extend anyway" would only spend (fix wave 3, N246). Nil for
// any other analysis.
func extendParentRefusal(an *schemas.Analysis) error {
	if an == nil || an.Verdict.Decision != schemas.DecisionInvalid {
		return nil
	}
	reason := "see its result"
	if len(an.Verdict.InvalidReasons) > 0 {
		reason = cmp.Or(an.Verdict.InvalidReasons[0], reason)
	}
	return fmt.Errorf("This comparison's test was broken (%s). Fix that first; a follow-up would be broken the same way.", reason)
}

// storedExtendRefusal is extendParentRefusal on the parent's newest stored
// analysis, read without analysing again; nil when it has none.
func storedExtendRefusal(ctx context.Context, env jobEnv, expID string) error {
	if env.App == nil || env.App.store == nil {
		return nil
	}
	an, err := env.App.store.LatestAnalysis(ctx, expID)
	if err != nil {
		return nil // an unreadable analysis is decided by the job's own analysis at run
	}
	return extendParentRefusal(an)
}
