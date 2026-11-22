package tenancy

// Issues to benchmark (lane G01, handlers/issuebench_scan.go): the scan is
// a free dry run over the org's GitHub connection; members may run it.
func init() {
	PlatformPolicy[key("POST", api+"/issuebench/scan")] = org(member)
}
