package modelswitch

import (
	"context"
	"net/http"
)

// Get reads one management path of the gateway into out, through the same
// address checks and credential handling as the apply calls.
func (g Gateway) Get(ctx context.Context, path string, out any) error {
	return g.call(ctx, http.MethodGet, path, nil, out)
}
