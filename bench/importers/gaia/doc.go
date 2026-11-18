// Package gaia imports the validation split of GAIA (General AI
// Assistants, gaia-benchmark/GAIA on Hugging Face) as an answer suite on the
// research track (docs/importers-research.md).
//
// Each question becomes one answer task: the question is the instruction,
// an attached file is copied into the workspace, the level becomes a tag and
// the answer category, and the final answer is graded by answer_match in
// its gaia mode (GAIA's quasi-exact match, package gaiascore). Questions
// whose annotators needed the web are refused, since runs have no internet,
// unless the user supplies a recorded Tavily cassette that covers them; the
// task then gets the research track's websearch tool, replayed from that
// cassette. The test split is refused: its answers are private.
//
// GAIA is gated on Hugging Face. This package never downloads it: the user
// accepts the terms and downloads the files, then imports the local copy.
// It is tested against a hand-made fixture in testdata/ with the documented
// shape and invented questions.
package gaia
