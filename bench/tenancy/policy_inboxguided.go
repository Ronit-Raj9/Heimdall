package tenancy

// Guided inbox accept (lane Y14): bulk accept writes task folders like the
// single accept.
func init() {
	OrgPolicy[key("POST", api+"/inbox/accept")] = org(member)
}
