// Package harbor imports tasks in the Harbor format (task.toml,
// instruction.md, environment/, solution/solve.sh, tests/test.sh), as
// Terminal-Bench 2.x, SkillsBench 1.0 and Harbor hub datasets publish them,
// and SkillsBench's later task.md packages, which differ only in file names.
//
// Each task keeps its own image (Task.Environment, built from the upstream
// environment folder) and is graded by its upstream test script in a
// snapshot of the agent's container. The bench verifier has no network, so
// the tools the test script downloads are installed at image build instead,
// and tasks that need the network in any other way are skipped with the
// reason. docs/importing-harbor.md is the user guide; docs/scenarios-design.md
// the contract.
package harbor
