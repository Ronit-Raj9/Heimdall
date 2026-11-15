package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const adapterFaultFile = "adapter.fault.json"

type adapterFaultSource struct{}

func (s *adapterFaultSource) GetName() string    { return "adapter_fault" }
func (s *adapterFaultSource) GetVersion() string { return "1.0.0" }
func (s *adapterFaultSource) Collect(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if rc == nil || rc.Probe == nil || rc.Probe.Layer != schemas.LayerAdapter {
		return nil
	}
	if rc.Dir == "" {
		return fmt.Errorf("read adapter fault evidence: no run directory")
	}
	b, err := os.ReadFile(filepath.Join(rc.Dir, adapterFaultFile))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read adapter fault evidence: %w", err)
	}
	if err != nil {
		return err
	}
	var out schemas.AdapterFaultEvidence
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("parse adapter fault evidence: %w", err)
	}
	if out.RunID == "" {
		return fmt.Errorf("adapter fault evidence has no run id")
	}
	ruleHash, err := utils.HashJSON(rc.Probe.Apply.AdapterFault)
	if err != nil {
		return err
	}
	if err := utils.VerifyAdapterFaultEvidence(&out, rc.Run.RunID, ruleHash); err != nil {
		return fmt.Errorf("verify adapter fault evidence: %w", err)
	}
	ev.AdapterFault = &out
	seq := newEventSeq(rc, schemas.SourceAdapterFault, schemas.EvidenceGateway)
	for _, row := range out.Entries {
		if row.Phase != "decision" {
			continue
		}
		e := seq.event(schemas.EventFault, row.At)
		e.AttemptID, e.ToolName, e.Target = row.OperationID, row.Name, row.Kind
		e.Summary, e.IsError = row.Mode, row.Mode != "pass"
		if e.IsError {
			e.Status = schemas.EventStatusError
		} else {
			e.Status = schemas.EventStatusSuccess
		}
		e.EvidenceSource, e.EvidenceStrength = schemas.EvidenceSourceAdapterFault, schemas.EvidenceStrong
		ev.Events = append(ev.Events, e)
	}
	if err := writeEvents(rc, ev); err != nil {
		return err
	}
	if !out.Complete {
		return fmt.Errorf("adapter fault evidence incomplete: %s", out.Reason)
	}
	return nil
}
