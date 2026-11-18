// Package browsecompplus imports BrowseComp-Plus (Tevatron/browsecomp-plus
// on Hugging Face, github.com/texttron/BrowseComp-Plus) as a research-track
// suite over a closed corpus (docs/importers-research.md).
//
// The published rows are obfuscated with BrowseComp's canary; the importer
// decrypts them in memory (crypt.go, the authors' scheme) and writes the
// answer only in the obfuscated form upstream publishes. The suite's corpus
// is the gold, evidence and hard-negative pages of the imported queries,
// served by a Tavily-shaped search service (assets/corpus/server.py) that the
// research track's websearch tool reaches through the tool proxy, so every
// search lands in the run's ledger and evidence_recall can read it. Short
// exact answers are graded by a deterministic verifier
// (assets/verifier/verify.py); the others go to a judged suite, which the
// importer writes only inside a datasets/ folder because the judge reads its
// answers in plain text.
package browsecompplus
