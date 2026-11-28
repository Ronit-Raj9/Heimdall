package handlers

import "testing"

// The names the run page's Downloads section offers (ui/components/runs/runFileDownloads.tsx)
// must be names the server serves; a name it refuses would be a dead button.
func TestRunPageDownloadNamesServed(t *testing.T) {
	for _, n := range []string{"run.json", "grade.json", "metrics.json", "cost.json", "gates.json", "receipts.json", "events.jsonl", "workspace.diff", "seal.json"} {
		if _, ok := runFileTypes[n]; !ok {
			t.Errorf("run file %q is not served by /files/{name}", n)
		}
	}
	for _, n := range []string{"gateway.requests.jsonl", "gateway.raw.jsonl", "tools.jsonl", "proxy.ledger.jsonl", "agent.stream.jsonl", "agent.stderr.txt"} {
		if !rawEvidenceNameOK(n) {
			t.Errorf("raw evidence %q is not served by /evidence/{name}", n)
		}
	}
}
