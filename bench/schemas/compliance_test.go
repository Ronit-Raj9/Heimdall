package schemas

import (
	"encoding/json"
	"testing"
)

func TestCompliancePackOmitsEmptyOptionalFields(t *testing.T) {
	cases := []struct {
		name   string
		pack   CompliancePack
		absent []string
	}{
		{"no release, nothing unmapped", CompliancePack{}, []string{"release", "unmapped", "warnings"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.pack)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			for _, k := range c.absent {
				if _, ok := m[k]; ok {
					t.Errorf("%s present in %s", k, b)
				}
			}
			for _, k := range []string{"version", "tests", "nist_functions", "record_keeping", "not_certified"} {
				if _, ok := m[k]; !ok {
					t.Errorf("%s missing in %s", k, b)
				}
			}
		})
	}
}
