package schemas

// ValueState says whether a measured number was observed.
type ValueState string

// Value states. Unknown is never rendered or aggregated as zero.
const (
	ValueObserved      ValueState = "observed"
	ValueUnknown       ValueState = "unknown"
	ValueNotApplicable ValueState = "not_applicable"
)

// EvidenceLevel is where a value was observed (DESIGN.md 9.5).
type EvidenceLevel string

// Evidence levels, from weakest coverage requirement to strongest detail.
const (
	EvidenceOutcome  EvidenceLevel = "outcome"  // verifier and the engine's clock
	EvidenceGateway  EvidenceLevel = "gateway"  // Bifrost rows and the proxy ledger
	EvidenceNative   EvidenceLevel = "native"   // the agent's own stream, through a parser
	EvidenceBoundary EvidenceLevel = "boundary" // tool shims and fixture counters
)

// Value is every measured number in the bench. Use the constructors.
type Value struct {
	State         ValueState       `json:"state"`
	V             *float64         `json:"v"`
	Evidence      EvidenceLevel    `json:"evidence,omitempty"` // legacy coverage tier
	Source        EvidenceOrigin   `json:"source,omitempty"`
	EvidenceLevel EvidenceStrength `json:"evidence_level,omitempty"`
	Bounds        *[2]float64      `json:"bounds,omitempty"`
	Reason        string           `json:"reason,omitempty"`
}

// Observed returns an observed value.
func Observed(v float64, ev EvidenceLevel) Value {
	return Value{State: ValueObserved, V: &v, Evidence: ev}
}

// Unknown returns an unknown value with the reason it could not be observed.
func Unknown(reason string, ev EvidenceLevel) Value {
	return Value{State: ValueUnknown, Evidence: ev, Reason: reason}
}

// UnknownWithin returns an unknown value known to lie within [lo, hi].
func UnknownWithin(reason string, ev EvidenceLevel, lo, hi float64) Value {
	return Value{State: ValueUnknown, Evidence: ev, Reason: reason, Bounds: &[2]float64{lo, hi}}
}

// NotApplicable returns a value for a measurement that does not apply.
func NotApplicable() Value {
	return Value{State: ValueNotApplicable}
}

// Float returns the number and true only when the value was observed.
func (v Value) Float() (float64, bool) {
	if v.State != ValueObserved || v.V == nil {
		return 0, false
	}
	return *v.V, true
}

// IsObserved reports whether the value carries an observed number.
func (v Value) IsObserved() bool {
	_, ok := v.Float()
	return ok
}
