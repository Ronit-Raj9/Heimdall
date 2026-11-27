package handlers

// The archive mark (UI fix wave D1) never reaches a shared page: a link to
// an archived evaluation, comparison or run keeps working exactly as before,
// and its visitor is not told the item was archived or by whom.
var d1SharedHidden = []string{"archived_at", "archived_by"}

func withoutD1SharedFields(v any) any {
	if m, ok := v.(map[string]any); ok {
		for _, k := range d1SharedHidden {
			delete(m, k)
		}
	}
	return v
}
