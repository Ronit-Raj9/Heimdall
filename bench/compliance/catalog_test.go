package compliance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

var idPatterns = map[string]*regexp.Regexp{
	schemas.FrameworkOWASPLLM2025:     regexp.MustCompile(`^LLM(0[1-9]|10):2025$`),
	schemas.FrameworkOWASPAgentic2026: regexp.MustCompile(`^ASI(0[1-9]|10)$`),
	schemas.FrameworkNISTAIRMF:        regexp.MustCompile(`^(GOVERN|MAP|MEASURE|MANAGE) [0-9]+\.[0-9]+$`),
	schemas.FrameworkEUAIAct:          regexp.MustCompile(`^Art\. [0-9]+(\([0-9]+\))?$`),
}

func TestCatalogIDsWellFormed(t *testing.T) {
	for id, e := range catalog {
		re, ok := idPatterns[e.framework]
		if !ok {
			t.Errorf("%s: unknown framework %q", id, e.framework)
			continue
		}
		if !re.MatchString(id) || e.id != id || e.name == "" || !strings.HasPrefix(e.source, "https://") {
			t.Errorf("catalog entry %q malformed: %+v", id, e)
		}
		if e.framework == schemas.FrameworkOWASPAgentic2026 && e.verified {
			t.Errorf("%s: agentic ids come from a secondary source and must stay unverified", id)
		}
	}
}

func TestRulesCiteCatalogIDs(t *testing.T) {
	tables := map[string]map[string]rule{"probe": probeRules, "axis": axisRules, "family": familyRules, "gate": gateRules}
	for name, table := range tables {
		for key, r := range table {
			if r.title == "" || r.note == "" || len(r.ids) == 0 {
				t.Errorf("%s rule %s: title, note and ids are required", name, key)
			}
			for _, id := range r.ids {
				if _, ok := catalog[id]; !ok {
					t.Errorf("%s rule %s cites %s, which is not in the catalog", name, key, id)
				}
			}
		}
	}
	for _, id := range packLevelIDs {
		if _, ok := catalog[id]; !ok {
			t.Errorf("pack-level id %s is not in the catalog", id)
		}
	}
}

// TestBuiltinProbesMapped checks every probe shipped in the bench's own
// suites is mapped by id or at least by axis, so a new probe is never silently
// unmapped.
func TestBuiltinProbesMapped(t *testing.T) {
	files, err := filepath.Glob("../benchmarks/*/probes/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no built-in probes found")
	}
	idRe := regexp.MustCompile(`(?m)^id:\s*(\S+)`)
	axisRe := regexp.MustCompile(`(?m)^axis:\s*(\S+)`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		id, axis := match(idRe, b), match(axisRe, b)
		test := conditionTest(id, &schemas.ProbeSpec{ID: id, Axis: axis})
		if test.MappedBy == "none" {
			t.Errorf("probe %s (%s, axis %q) has no mapping", id, f, axis)
		}
	}
}

// TestGateNamesMapped lists the gate names the gates package ships; the cmd
// package registers them, and this keeps the table in step by name.
func TestGateNamesMapped(t *testing.T) {
	src, err := filepath.Glob("../gates/*.go")
	if err != nil {
		t.Fatal(err)
	}
	constRe := regexp.MustCompile(`(?m)^\s*(?:const )?([A-Z][A-Za-z]*) += "([a-z0-9_]+)"`)
	getName := regexp.MustCompile(`GetName\(\) string \{ return ([A-Za-z]+) \}`)
	consts := map[string]string{}
	var used []string
	for _, f := range src {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range constRe.FindAllSubmatch(b, -1) {
			consts[string(m[1])] = string(m[2])
		}
		for _, m := range getName.FindAllSubmatch(b, -1) {
			used = append(used, string(m[1]))
		}
	}
	for _, c := range used {
		name, ok := consts[c]
		if !ok {
			continue
		}
		if _, ok := gateRules[name]; !ok {
			// Sibling areas add gates while this table is maintained by hand;
			// an unmapped gate is listed in the pack's unmapped ids, so it is
			// reported, never dropped. The gates below must stay mapped.
			if mustMap[name] {
				t.Errorf("gate %s (%s) has no mapping", name, c)
			} else {
				t.Logf("gate %s (%s) has no mapping yet; packs list it as unmapped", name, c)
			}
		}
	}
}

// mustMap are the gates that shipped before the compliance pack; each must
// keep a mapping.
var mustMap = map[string]bool{
	"g1_protected_paths": true, "g2_test_infra": true, "g4_regrade": true, "g6_null_agents": true, "egress": true,
	"upstream_errors": true, "impossible_pass": true, "g8_false_completion": true, "model_fidelity": true,
	"g10_destroyed_user_work": true, "g11_protected_path": true, "g12_injection_followed": true,
	"g13_memory_trap_followed": true, "tool_injection_followed": true, "canary_echoed": true,
	"insecure_code_added": true, // X4
}

func match(re *regexp.Regexp, b []byte) string {
	m := re.FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
}
