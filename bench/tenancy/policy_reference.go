package tenancy

// Reference board (gap G4-03, handlers/reference.go): the published scores
// of the operator's reference runs are public and read-only. No org is
// resolved; the board holds no organization's own data.
func init() {
	PlatformPolicy[key("GET", api+"/reference/board")] = public()
}
