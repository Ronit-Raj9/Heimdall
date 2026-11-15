// Package tau2 imports the customer service domains of tau2-bench (airline,
// retail) as a suite of track tool_use with output kind state.
//
// Each task runs the domain as an MCP service built from the upstream package
// (image/tau2svc.py): the domain's tools on a fresh database seeded for the
// task, plus respond_to_user, which returns the reply of a scripted customer
// built from the task's user scenario. The service exports the final
// database and the conversation; the verifier, in the same image, replays the
// task's expected actions on a fresh database and compares the two as
// upstream does. docs/importing-tau2.md and docs/scenarios-design.md explain
// the format and what differs from upstream.
package tau2
