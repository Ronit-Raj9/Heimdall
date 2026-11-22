package deploy

// Lane Z14: the monitoring files (hosted/monitoring/) stay true to the
// server: every metric an alert, SLO or dashboard panel reads is one the
// bench code registers, slos.yml and slo-rules.yml name the same SLOs, and
// every runbook link has its section in docs/monitoring.md.

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const monDir = "hosted/monitoring"

var metricRe = regexp.MustCompile(`\bheimdall_[a-z_]+`)

// registeredMetrics are the heimdall_* names the bench code registers.
func registeredMetrics(t *testing.T) map[string]bool {
	t.Helper()
	lit := regexp.MustCompile(`"(heimdall_[a-z_]+)"`)
	out := map[string]bool{}
	err := filepath.WalkDir("..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "ui" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range lit.FindAllStringSubmatch(string(raw), -1) {
			out[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func checkExpr(t *testing.T, where, expr string, known map[string]bool) {
	t.Helper()
	for _, name := range metricRe.FindAllString(expr, -1) {
		base := name
		for _, suf := range []string{"_bucket", "_count", "_sum"} {
			if strings.HasSuffix(name, suf) && known[strings.TrimSuffix(name, suf)] {
				base = strings.TrimSuffix(name, suf)
			}
		}
		if !known[base] {
			t.Errorf("%s reads %s, which no bench code registers", where, name)
		}
	}
}

type ruleFile struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert       string            `yaml:"alert"`
			Record      string            `yaml:"record"`
			Expr        string            `yaml:"expr"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

func readYAML(t *testing.T, name string, into any) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(monDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(raw)
}

func TestMonitoringFilesMatchTheServer(t *testing.T) {
	known := registeredMetrics(t)
	for _, must := range []string{"heimdall_http_request_duration_seconds", "heimdall_remote_leases_ended_total", "heimdall_evaluation_start_seconds"} {
		if !known[must] {
			t.Fatalf("metric scan found no %s; the walk is broken", must)
		}
	}
	docs, err := os.ReadFile("../docs/monitoring.md")
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]bool{}
	for _, line := range strings.Split(string(docs), "\n") {
		if strings.HasPrefix(line, "#") {
			h := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#")))
			anchors[strings.ReplaceAll(regexp.MustCompile(`[^a-z0-9 -]`).ReplaceAllString(h, ""), " ", "-")] = true
		}
	}

	var slos struct {
		SLOs []struct {
			Name      string  `yaml:"name"`
			Objective float64 `yaml:"objective"`
			SLI       struct {
				Events struct {
					Error string `yaml:"error_query"`
					Total string `yaml:"total_query"`
				} `yaml:"events"`
			} `yaml:"sli"`
		} `yaml:"slos"`
	}
	texts := map[string]string{"slos.yml": readYAML(t, "slos.yml", &slos)}
	sloNames := map[string]bool{}
	for _, s := range slos.SLOs {
		sloNames[s.Name] = true
		checkExpr(t, "slos.yml "+s.Name, s.SLI.Events.Error+" "+s.SLI.Events.Total, known)
		if s.Objective <= 0 || s.Objective >= 100 {
			t.Errorf("slo %s objective %v", s.Name, s.Objective)
		}
		if !anchors["slo-"+s.Name] {
			t.Errorf("docs/monitoring.md has no section \"SLO %s\"", s.Name)
		}
	}

	ruleSLOs := map[string]bool{}
	for _, f := range []string{"alerts.yml", "slo-rules.yml"} {
		var rf ruleFile
		texts[f] = readYAML(t, f, &rf)
		if len(rf.Groups) == 0 {
			t.Fatalf("%s has no rule groups", f)
		}
		for _, g := range rf.Groups {
			for _, r := range g.Rules {
				where := f + " " + r.Alert + r.Record
				checkExpr(t, where, r.Expr, known)
				if r.Labels["sloth_slo"] != "" {
					ruleSLOs[r.Labels["sloth_slo"]] = true
				}
				if r.Alert == "" {
					continue
				}
				if sev := r.Labels["severity"]; sev != "page" && sev != "ticket" && sev != "info" {
					t.Errorf("%s: severity %q", where, sev)
				}
				url := r.Annotations["runbook_url"]
				_, anchor, _ := strings.Cut(url, "#")
				if !strings.HasPrefix(url, "docs/monitoring.md#") || !anchors[anchor] {
					t.Errorf("%s: runbook %q has no section in docs/monitoring.md", where, url)
				}
			}
		}
	}
	if !equalKeys(sloNames, ruleSLOs) {
		t.Errorf("slos.yml names %v but slo-rules.yml covers %v", keys(sloNames), keys(ruleSLOs))
	}

	raw, err := os.ReadFile(filepath.Join(monDir, "grafana/dashboards/heimdall.json"))
	if err != nil {
		t.Fatal(err)
	}
	texts["heimdall.json"] = string(raw)
	var dash struct {
		UID    string `json:"uid"`
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatal(err)
	}
	if dash.UID == "" || len(dash.Panels) < 10 {
		t.Fatalf("dashboard uid %q with %d panels", dash.UID, len(dash.Panels))
	}
	sloRe := regexp.MustCompile(`sloth_slo="([a-z-]+)"`)
	for _, p := range dash.Panels {
		for _, tg := range p.Targets {
			checkExpr(t, "dashboard panel "+p.Title, tg.Expr, known)
			for _, m := range sloRe.FindAllStringSubmatch(tg.Expr, -1) {
				if !sloNames[m[1]] {
					t.Errorf("dashboard panel %s reads unknown SLO %s", p.Title, m[1])
				}
			}
		}
	}
	for _, f := range []string{"prometheus.yml", "otel-collector.yaml", "tempo.yaml"} {
		var doc map[string]any
		texts[f] = readYAML(t, f, &doc)
	}
	for f, s := range texts {
		if strings.ContainsAny(s, "\u2014\u2013") {
			t.Errorf("%s has an em or en dash", f)
		}
	}
}

func equalKeys(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
