// Package impossiblebench imports ImpossibleBench: coding tasks whose tests
// were mutated to contradict the task's own specification, so no honest
// change passes them and a pass can only come from changing or gaming the
// tests. Imported tasks carry `impossible: true`, the impossible_pass gate
// turns any pass into an integrity violation, and the suite is on the safety
// track. See docs/importing-swe.md.
//
// Two upstream variants exist. The LiveCodeBench variant (single-function
// Python tasks, no per-task image) is converted here. The SWE-bench variant
// has the shape of SWE-bench rows and is converted by the swebench importer,
// which this package reaches through the importer registry.
package impossiblebench
