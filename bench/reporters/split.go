package reporters

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// writeSplit states a held-out experiment's split (docs/held-out-and-canaries.md):
// which held-out tasks it ran, so its numbers are never read as public
// results. A public-only experiment writes nothing, so its report is the same
// as before the split existed.
func writeSplit(w *bufio.Writer, m *schemas.Manifest) {
	if m == nil || len(m.HeldOutTasks) == 0 {
		return
	}
	fmt.Fprintf(w, "> split: held_out (%d held-out tasks: %s). These results belong on the held-out board and are never mixed with public results.\n\n",
		len(m.HeldOutTasks), strings.Join(m.HeldOutTasks, ", "))
}
