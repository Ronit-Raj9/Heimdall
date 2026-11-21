package handlers

import "github.com/valyala/fasthttp"

// Project files area (projectfiles.go) route descriptions.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/project/files", Tag: "project", Summary: "List the project areas, or one area's folder",
			Query: []QueryDoc{
				{Name: "area", Description: "agents, benchmarks, experiments, policies or uploads; empty lists the areas"},
				{Name: "dir", Description: "Folder inside the area; default its top"},
				{Name: "recursive", Description: "true lists every file below dir"},
			}, Response: ProjectFileList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/project/files/{area}/{path:*}", Tag: "project", Summary: "Read a file (text, or base64 when binary; ETag is its sha256) or list a folder",
			Query:    []QueryDoc{{Name: "raw", Description: "true downloads the file's bytes"}, {Name: "recursive", Description: "On a folder, list every file below it"}},
			Response: ProjectFile{}, AlsoRaw: "application/octet-stream"},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/project/files/{area}/{path:*}", Tag: "project", Summary: "Save a file atomically (at most 1 MiB); YAML is validated, every save is versioned; If-Match (sha256) gives 409 when the file changed",
			Auth: AuthWrite, RawRequest: "application/octet-stream", Query: []QueryDoc{{Name: "note", Description: "Short note stored with the version"}, {Name: "if_match", Description: "Expected sha256, same as the If-Match header"}},
			Response: ProjectFileSaved{}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/project/files/{area}/{path:*}", Tag: "project", Summary: "Delete a file or folder; the deletion is recorded in the file's versions",
			Auth: AuthWrite, Query: []QueryDoc{{Name: "if_match", Description: "Expected sha256, same as the If-Match header"}}, Response: ProjectFileSaved{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/project/versions/{area}/{path:*}", Tag: "project", Summary: "A file's saved versions, newest first; with id, that version's content",
			Query: []QueryDoc{{Name: "id", Description: "Version id; returns its content as a ProjectFile"}}, Response: ProjectFileVersions{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/project/export.zip", Tag: "project", Summary: "Download the whole project (every area) as a zip",
			Raw: "application/zip", Status: fasthttp.StatusOK},
	)
}
