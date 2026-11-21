package tenancy

// F4 key delivery (lane L05): only a runner of the organization reaches the
// lease credentials route; the handler also checks the runner holds the lease.
func init() {
	OrgPolicy[key("GET", api+"/workers/leases/{lease_id}/credentials")] = runner(false)
}
