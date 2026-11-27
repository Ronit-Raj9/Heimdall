package docs

import "strings"

// Category is one group of the in-app Help list (fix wave M8-64).
type Category struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Categories lists the groups in the order the Help list shows them.
var Categories = []Category{
	{ID: "start", Label: "Start here"},
	{ID: "website", Label: "Using the website"},
	{ID: "importing", Label: "Importing benchmarks"},
	{ID: "safety", Label: "Safety"},
	{ID: "voice", Label: "Voice agents"},
	{ID: "guides", Label: "More guides"},
}

// startHere are the guides worth reading first, in reading order. They
// match the pinned list of the Help page.
var startHere = []string{
	"web-quickstart", "glossary", "web-agents", "web-suites", "web-experiments",
	"web-analysis", "web-keys-secrets", "web-runners", "web-integrations",
	"web-sync", "web-admin", "quickstart", "adding-an-agent", "bring-your-own",
	"frameworks", "hosting", "runners",
}

// StartRank is the place of name in the "start" group, or -1.
func StartRank(name string) int {
	for i, n := range startHere {
		if n == name {
			return i
		}
	}
	return -1
}

// CategoryOf is the id of the group guide name belongs to; every name has
// one ("guides" when no rule matches).
func CategoryOf(name string) string {
	switch {
	case StartRank(name) >= 0:
		return "start"
	case strings.HasPrefix(name, "web-"):
		return "website"
	case strings.HasPrefix(name, "importing-"), strings.HasPrefix(name, "importers-"):
		return "importing"
	case strings.Contains(name, "safety"), name == "red-team", name == "eval-awareness", name == "egress":
		return "safety"
	case strings.HasPrefix(name, "voice-"):
		return "voice"
	}
	return "guides"
}
