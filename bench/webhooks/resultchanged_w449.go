package webhooks

// EventResultChanged (W4-49 E): a re-score changed a comparison's result.
// Body {exp_id, regrade_id, before, after, note}, one event per new flipped
// entry of the validity ledger.
const EventResultChanged = "result.changed"

func init() { Events = append(Events, EventResultChanged) }
