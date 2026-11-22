package engine

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// sealedVerifier is a store that can check a run it sealed a moment ago
// without listing the experiment's slots or reading all of seals.log
// (store.FSStore.VerifySealed, INFRA_I4 item 1).
type sealedVerifier interface {
	VerifySealed(ctx context.Context, run *schemas.Run) error
}

// verifyAccepted is the check the coordinator runs on an uploaded run right
// after sealing it. Full verification (Verify, VerifyChain) still runs at
// experiment end, in the evidence verify job and in mirror sweeps.
func verifyAccepted(ctx context.Context, st schemas.Store, run *schemas.Run) error {
	if v, ok := st.(sealedVerifier); ok {
		return v.VerifySealed(ctx, run)
	}
	return st.Verify(ctx, run.ExpID, run.RunID)
}
