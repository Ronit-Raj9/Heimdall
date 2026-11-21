package maxim

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceexport"
)

// RunInput is a sealed run to push: its identity and a reader of its files.
// The caller has verified the run against its seal.
type RunInput struct {
	RunID    string
	Slot     schemas.Slot
	SealHash string
	Files    traceexport.ReadFunc
	Auto     bool
}

// PushRun builds a sealed run's trace (no tool payloads: only their hashes
// and sizes leave Heimdall) and sends it to the connection's repository,
// then records the attempt. The returned push is recorded even when the
// send failed; the error is then also returned.
func PushRun(ctx context.Context, c Client, st *Store, s Settings, apiKey string, in RunInput, now time.Time) (Push, error) {
	runID := in.RunID
	p := Push{RunID: runID, ExpID: in.Slot.ExpID, TraceID: traceexport.TraceID(runID), At: now.UTC(), Auto: in.Auto}
	err := push(ctx, c, s, apiKey, in)
	p.Status = StatusPushed
	if err != nil {
		p.Status, p.Error = StatusFailed, err.Error()
	}
	if rerr := st.Record(p); rerr != nil {
		return p, errors.Join(err, rerr)
	}
	return p, err
}

func push(ctx context.Context, c Client, s Settings, apiKey string, in RunInput) error {
	if apiKey == "" {
		return errors.New("no maxim api key is stored")
	}
	if in.Files == nil {
		return errors.New("no run files")
	}
	tin, err := traceexport.LoadFiles(in.Files)
	if err != nil {
		return fmt.Errorf("load run: %w", err)
	}
	tin.Slot = in.Slot
	if in.SealHash != "" {
		tin.SealHash = in.SealHash
	}
	td, err := traceexport.Build(tin, traceexport.Options{})
	if err != nil {
		return fmt.Errorf("build trace: %w", err)
	}
	return c.PushTrace(ctx, apiKey, s.RepoID, td)
}
