package spec

import (
	"encoding/json"
	"testing"
)

func TestSchemasEmbedded(t *testing.T) {
	for _, name := range []string{"suite", "task", "probe", "agent", "experiment"} {
		t.Run(name, func(t *testing.T) {
			file := name + ".schema.json"
			b, err := FS.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Schema               string `json:"$schema"`
				ID                   string `json:"$id"`
				AdditionalProperties *bool  `json:"additionalProperties"`
			}
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatalf("%s is not valid JSON: %v", file, err)
			}
			if doc.Schema != "https://json-schema.org/draft/2020-12/schema" {
				t.Errorf("$schema = %q", doc.Schema)
			}
			if doc.ID != "https://github.com/maximhq/bifrost/bench/spec/"+file {
				t.Errorf("$id = %q", doc.ID)
			}
			if doc.AdditionalProperties == nil || *doc.AdditionalProperties {
				t.Errorf("root additionalProperties must be false")
			}
		})
	}
}
