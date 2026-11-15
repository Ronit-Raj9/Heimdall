// Package swebench imports SWE-bench-style tasks: a repository at a base
// commit inside a per-instance image, a problem statement, a gold patch and a
// test patch with the FAIL_TO_PASS and PASS_TO_PASS test ids. It reads the
// rows of SWE-rebench, SWE-bench-Live, Impossible-SWEbench and the original
// SWE-bench, exported to JSONL, and writes a suite whose tasks run in the
// instance image and are graded in a clean room by the instance's own test
// command and log parser. See docs/importing-swe.md.
package swebench
