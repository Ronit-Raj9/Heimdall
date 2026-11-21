package handlers

// Usage and spend area (web parity L25): route docs.
func init() {
	usageQuery := []QueryDoc{
		{Name: "from", Description: "First day, YYYY-MM-DD (UTC); default 29 days before to"},
		{Name: "to", Description: "Last day, inclusive, YYYY-MM-DD (UTC); default today"},
		{Name: "group_by", Description: "day (default), model, agent or key"},
	}
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: APIPrefix + "/usage", Tag: "usage",
		Summary: "Run cost and tokens over a date range, grouped, plus provider key spend", Query: usageQuery, Response: UsageReport{}})
	OrgRouteDocs = append(OrgRouteDocs, RouteDoc{Method: "GET", Path: APIPrefix + "/admin/usage", Tag: "usage",
		Summary: "Platform usage per org (server admins only)", Query: usageQuery[:2], Response: AdminUsageReport{}})
}
