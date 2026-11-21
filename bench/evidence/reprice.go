package evidence

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// unpricedReasons are the cost row reasons priceUsage writes when the
// snapshot has no price for the model. Only such rows are repriced: a row
// whose usage was not recorded, or that failed without a status, stays
// unknown whatever price the org gives.
var unpricedReasons = []string{"no price for model", "no price table"}

// Unpriced reports whether a cost row is unknown only because the pinned
// snapshot had no price for its model.
func Unpriced(row schemas.CostRow) bool {
	if row.USD.State != schemas.ValueUnknown || row.FaultInject {
		return false
	}
	for _, r := range unpricedReasons {
		if strings.HasPrefix(row.USD.Reason, r) {
			return true
		}
	}
	return false
}

// RepriceCost recomputes a run's total cost from its cost rows, pricing the
// rows the snapshot could not price with lookup (the organization's own
// price for the model, set on its provider key). It returns the total and
// true only when every row is then known and at least one row was priced by
// lookup; otherwise the run's cost stays as the metric recorded it. Rows
// that do not apply add nothing, as in the cost_usd metric.
func RepriceCost(rows []schemas.CostRow, lookup func(model string) (ModelPrice, bool)) (schemas.Value, bool) {
	if lookup == nil || len(rows) == 0 {
		return schemas.Value{}, false
	}
	total, repriced := 0.0, 0
	for _, row := range rows {
		if v, ok := row.USD.Float(); ok {
			total += v
			continue
		}
		if row.USD.State == schemas.ValueNotApplicable {
			continue
		}
		if !Unpriced(row) {
			return schemas.Value{}, false
		}
		p, ok := lookup(row.Model)
		if !ok {
			return schemas.Value{}, false
		}
		total += p.Cost(row.Tokens)
		repriced++
	}
	if repriced == 0 {
		return schemas.Value{}, false
	}
	v := schemas.Observed(total, schemas.EvidenceGateway)
	v.Reason = fmt.Sprintf("%d of %d cost rows priced from your key's price", repriced, len(rows))
	return v, true
}
