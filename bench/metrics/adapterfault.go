package metrics

import (
	"context"
	"github.com/maximhq/bifrost/bench/schemas"
)

// AdapterFaultIssued counts controller decisions.
const AdapterFaultIssued = "adapter_faults_issued"
// AdapterFaultRecovery measures later unmodified success after a fault.
const AdapterFaultRecovery = "adapter_fault_recovery_rate"

type adapterFaultIssuedMetric struct{}

func (m *adapterFaultIssuedMetric) GetName() string    { return AdapterFaultIssued }
func (m *adapterFaultIssuedMetric) GetVersion() string { return version }
func (m *adapterFaultIssuedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{Name: AdapterFaultIssued, Version: version, Label: "Adapter faults issued", Kind: schemas.KindCount, HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary, Description: "Controller-issued adapter fault decisions averaged over runs with complete controller evidence.", PS2: false}
}
func (m *adapterFaultIssuedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if ev == nil || ev.AdapterFault == nil {
		return schemas.NotApplicable(), nil
	}
	n := 0
	for _, e := range ev.AdapterFault.Entries {
		if e.Phase == "decision" {
			n++
		}
	}
	if !ev.AdapterFault.Complete {
		return schemas.Unknown(ev.AdapterFault.Reason, schemas.EvidenceBoundary), nil
	}
	return schemas.Observed(float64(n), schemas.EvidenceBoundary), nil
}

type adapterFaultRecoveryMetric struct{}

func (m *adapterFaultRecoveryMetric) GetName() string    { return AdapterFaultRecovery }
func (m *adapterFaultRecoveryMetric) GetVersion() string { return version }
func (m *adapterFaultRecoveryMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{Name: AdapterFaultRecovery, Version: version, Label: "Adapter fault recovery", Kind: schemas.KindRate, HigherIsBetter: true, MinEvidence: schemas.EvidenceNative, Description: "Later successful unmodified operations averaged over complete controller episodes.", PS2: false}
}
func (m *adapterFaultRecoveryMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if ev == nil || ev.AdapterFault == nil {
		return schemas.NotApplicable(), nil
	}
	if !ev.AdapterFault.Complete {
		return schemas.Unknown(ev.AdapterFault.Reason, schemas.EvidenceNative), nil
	}
	episodes := 0
	recovered := 0
	for i, e := range ev.AdapterFault.Entries {
		if e.Phase != "decision" || e.Mode == "pass" {
			continue
		}
		episodes++
		for _, later := range ev.AdapterFault.Entries[i+1:] {
			if later.Phase == "completed" && later.Kind == e.Kind && later.Name == e.Name && later.Success {
				recovered++
				break
			}
		}
	}
	if episodes == 0 {
		return schemas.NotApplicable(), nil
	}
	return schemas.Observed(float64(recovered)/float64(episodes), schemas.EvidenceNative), nil
}
