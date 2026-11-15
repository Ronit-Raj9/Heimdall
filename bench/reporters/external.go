package reporters

import (
	"bufio"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// writeExternal labels a report whose runs called real external hosts
// (experiment external: live or record, docs/external-apis.md), the way a
// synthetic report is labelled: a rerun can get different answers from the
// host, so the numbers are not repeatable. Replay runs are answered from a
// committed cassette and get no label. m may be nil.
func writeExternal(w *bufio.Writer, m *schemas.Manifest) {
	if !utils.ExternalUnrepeatable(m) {
		return
	}
	fmt.Fprintf(w, "> external APIs: %s (not repeatable). The runs called the real hosts, which can answer differently on a rerun; replay a recorded cassette to compare releases.\n\n", m.Experiment.External)
}
