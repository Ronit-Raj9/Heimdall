// Package agentvista imports AgentVista (hkust-nlp, arXiv 2602.23166;
// dataset Warrieryes/AgentVista on Hugging Face, CC-BY-4.0) as a judged
// answer suite on the research track.
//
// Each of the 209 questions becomes one answer task: the question is the
// instruction, its images (1 to 10) are copied into the workspace, the
// domain and subdomain become tags, and the answer is graded by the judge
// grader with the agentvista-v1 prompt, which applies AgentVista's verifier
// rule (the prediction is correct when it matches the ground truth in
// meaning; synonyms and paraphrases count). Upstream's web_search and visit
// tools (Serper and Jina, paid keys) are replaced by the research track's
// websearch MCP tool (web_search and fetch_page over the Tavily route), with
// an empty cassette: searches answer "not recorded" unless an experiment
// opts into external: live. Upstream's image search (text-to-image and
// reverse image search) has no counterpart here, so the subdomain that
// depends on it (Visual Location Identification) is tagged as a draft with
// that reason in DRAFT.md.
//
// The upstream code repository has no licence file, so nothing is copied
// from it: the converter (assets/extract.py) and the judge prompt are
// written for the bench. The parquet shards are converted once with
// assets/extract.py; this package reads its tasks.jsonl and images/.
package agentvista
