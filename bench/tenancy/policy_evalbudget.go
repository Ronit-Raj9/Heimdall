package tenancy

// Evaluation budget (handlers/evaluationbudget.go, G4-01): raising the
// budget of an evaluation the spend cap stopped, and continuing it, is
// member work, as resume is.
func init() {
	OrgPolicy[key("POST", api+"/evaluations/{id}/raise-budget")] = org(member)
}
