package main

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/jobs"
)

// orgModelKeyAtCreate refuses, when a paid drafting job is created on the
// hosted server, a provider the organization has no working model key for,
// so the person reads the refusal at once instead of in a failed job (fix
// wave 3, H15 review: Review drafting and Policy Draft scenarios join Tasks
// from your API). Local servers use their own gateway keys and are not
// checked.
func orgModelKeyAtCreate(ctx context.Context, env jobEnv, provider, keyID string) error {
	if !env.Hosted {
		return nil
	}
	if err := appTasksOrgKey(ctx, env, appTasksParams{Provider: provider, KeyID: keyID}); err != nil {
		return fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
	}
	return nil
}
