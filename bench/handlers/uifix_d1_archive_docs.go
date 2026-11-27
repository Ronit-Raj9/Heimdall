package handlers

// Route docs of archive and unarchive (UI fix wave D1, decision 6).
func init() {
	for _, k := range []struct{ path, tag, what, role string }{
		{"/evaluations/{id}", "evaluations", "a finished evaluation", "admin; 409 while it is queued or running"},
		{"/experiments/{id}", "experiments", "a comparison", "admin; 409 while it is queued or running. The sealed folder is not touched"},
		{"/runs/{id}", "runs", "a run", "admin; 409 while it is still running. The sealed folder is not touched"},
		{"/improvements/{id}", "improvements", "an improvement run", "admin; 409 while it is queued, running or waiting for approval"},
		{"/findings/{id}", "findings", "a finding", "member; its state does not change"},
	} {
		APIRoutes = append(APIRoutes,
			RouteDoc{Method: "POST", Path: APIPrefix + k.path + "/archive", Tag: k.tag,
				Summary: "Archive " + k.what + " (" + k.role + "): its list leaves it out unless archived=include or only, it still opens by id, its share links keep working, and nothing is deleted. Every computed number (leaderboards, verdicts, usage, spend) still counts it. Archiving an archived item keeps its first archived_at",
				Auth:    AuthWrite, OptionalBody: true, Response: ArchiveResult{}},
			RouteDoc{Method: "POST", Path: APIPrefix + k.path + "/unarchive", Tag: k.tag,
				Summary: "Unarchive " + k.what + " (same role as archive): it shows in its list again. Unarchiving an item that is not archived does nothing",
				Auth:    AuthWrite, OptionalBody: true, Response: ArchiveResult{}},
		)
	}
}
