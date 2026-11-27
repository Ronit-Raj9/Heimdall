package handlers

import (
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/benchcatalog"
)

// catalogSuiteTitles maps a suite id to the benchmark title the catalog
// gives it (the headline recipes: "hd-harbor-bfcl" -> its catalog title).
// A suite the catalog only lists under its own id has no title here. The
// recipes are compiled in, so the map is built once per process.
var catalogSuiteTitles = sync.OnceValue(func() map[string]string {
	out := map[string]string{}
	for _, e := range (benchcatalog.Source{}).List("") {
		id, title := strings.TrimSpace(e.SuiteID), strings.TrimSpace(e.Title)
		if id == "" || title == "" || title == id {
			continue
		}
		if _, seen := out[id]; !seen {
			out[id] = title
		}
	}
	return out
})

// suiteTitle is the catalog title of a suite, or "" when the catalog has none.
func suiteTitle(id string) string {
	if id == "" {
		return ""
	}
	return catalogSuiteTitles()[id]
}
