package adapters

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// externalVars adds the external API template variables (docs/external-apis.md):
// {{external.apis_json}}, always (an empty object when the run has no
// external APIs, so one config serves every task), and {{external.<id>.url}}
// for each external API, the tool proxy address the agent calls it at. The
// same JSON is in BENCH_EXTERNAL_APIS, which the engine sets.
func externalVars(vars map[string]string, rc *schemas.RunContext) error {
	js, err := utils.ExternalAPIsJSON(rc.Services)
	if err != nil {
		return err
	}
	vars["external.apis_json"] = js
	for id, u := range utils.ExternalServers(rc.Services) {
		vars["external."+id+".url"] = u
	}
	return nil
}
