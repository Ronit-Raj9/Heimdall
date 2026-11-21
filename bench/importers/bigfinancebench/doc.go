// Package bigfinancebench imports the public release of BigFinanceBench
// (RogoAI/big-finance-benchmark on Hugging Face, arXiv 2606.03829) as a
// judged answer suite on the research track (docs/importing-bigfinancebench.md).
//
// The public release is 50 questions, a stratified subset of the 928-item
// benchmark; the other 878 are private and held back by the authors. Each
// question becomes one answer task with the research track's websearch
// tool (live web; no recording ships upstream). The official point-weighted
// rubric and reference answer are kept verbatim in verifier/answer.yaml and
// graded by the judge grader's bigfinancebench-v1 prompt, adapted from the
// upstream big_finance_harness/grader.py: final_answer_correct decides the
// pass, the share of rubric points earned is the rubric_score dimension.
package bigfinancebench
