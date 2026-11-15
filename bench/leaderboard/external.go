package leaderboard

import (
	"github.com/maximhq/bifrost/bench/utils"
)

// ReasonExternalLive is the exclusion reason of runs whose experiment called
// real external hosts (external: live or record, docs/external-apis.md). A
// rerun can get different answers from the host, so such runs are not
// repeatable and stay off the board, the way synthetic runs stay off the live
// board. Runs answered from a cassette (replay) count normally.
const ReasonExternalLive = "external APIs live (not repeatable)"

// externalReason returns ReasonExternalLive for a record of a live or record
// experiment, "" otherwise.
func externalReason(r *record) string {
	if r.exp != nil && utils.ExternalUnrepeatable(r.exp.m) {
		return ReasonExternalLive
	}
	return ""
}
