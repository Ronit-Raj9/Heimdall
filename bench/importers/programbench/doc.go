// Package programbench imports ProgramBench (facebookresearch/ProgramBench,
// MIT): 200 tasks, each a compiled program plus its documentation in a
// per-task Docker image; the agent writes a new codebase whose compile.sh
// builds ./executable, and hidden pytest branches (Hugging Face
// programbench/ProgramBench-Tests) score it. Each task is staged as a
// Harbor-format folder (prebuilt upstream image, converted eval pipeline as
// test.sh) and imported by the harbor importer. docs/importing-programbench.md
// is the user guide.
package programbench
