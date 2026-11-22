package handlers

// Public site (lane Y08) route docs. Hosted only, so they join OrgRouteDocs
// (the hosted document). /downloads/{file}, /.well-known/security.txt and
// /mcp sit outside /api and are described in docs/downloads.md, like
// /healthz.
func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "GET", Path: PublicPrefix + "/overview", Tag: "public",
			Summary: "No sign-in: what this server offers (library totals, version, the commit its downloads were built at)", Response: PublicOverview{}},
		RouteDoc{Method: "GET", Path: PublicPrefix + "/benchmarks", Tag: "public",
			Summary: "No sign-in: the built-in benchmark library, read-only: description, task and ready counts, licences, packs. No task content", Response: PublicLibrary{}},
		RouteDoc{Method: "GET", Path: PublicPrefix + "/docs", Tag: "public",
			Summary: "No sign-in: the user guides (internal notes left out)", Response: DocList{}},
		RouteDoc{Method: "GET", Path: PublicPrefix + "/docs/{name}", Tag: "public",
			Summary: "No sign-in: one user guide as Markdown", Response: DocBody{}},
		RouteDoc{Method: "GET", Path: PublicPrefix + "/pricing", Tag: "public",
			Summary: "No sign-in: plans and their stated limits", Response: PublicPricing{}},
		RouteDoc{Method: "GET", Path: PublicPrefix + "/downloads", Tag: "public",
			Summary: "No sign-in: the command line tool, SDK packages and source tarball this server built at its commit, with sizes and SHA-256 sums; files are at /downloads/{name}", Response: DownloadsManifest{}},
	)
}
