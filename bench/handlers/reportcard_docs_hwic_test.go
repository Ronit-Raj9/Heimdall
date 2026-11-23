package handlers

import "testing"

// The agent report card documents the ?evaluation= query lane H12 added
// (fix wave 3, N206), so the API docs list it.
func TestAgentReportCardDocsEvaluationQuery(t *testing.T) {
	for _, d := range APIRoutes {
		if d.Method != "GET" || d.Path != APIPrefix+"/agents/{id}/report-card" {
			continue
		}
		for _, q := range d.Query {
			if q.Name == "evaluation" {
				return
			}
		}
		t.Fatalf("report card query docs %+v lack evaluation", d.Query)
	}
	t.Fatal("no GET /agents/{id}/report-card doc")
}
