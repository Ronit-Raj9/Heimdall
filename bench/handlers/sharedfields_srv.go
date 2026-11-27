package handlers

// Fields the UI fix wave added to answers that a shared page also reads
// (sharedParts). What a shared page shows is the owner's decision, so they
// are taken out of the shared payload until the owner allows them:
//
//	evaluation  children[].cause      why a benchmark was skipped (named)
//	run         task_title, suite_title
var sharedHiddenFields = map[string]struct {
	top      []string
	children []string
}{
	"evaluation": {children: []string{"cause"}},
	"run":        {top: []string{"task_title", "suite_title"}},
}

// withoutNewSharedFields is part v of a shared page with the wave's new
// fields of that part removed; other parts are returned as they are.
func withoutNewSharedFields(part string, v any) any {
	v = withoutCGSharedFields(part, v)
	v = withoutD1SharedFields(v) // UI fix wave D1: archived_at, archived_by (uifix_d1_sharedfields.go)
	hide, ok := sharedHiddenFields[part]
	m, isMap := v.(map[string]any)
	if !ok || !isMap {
		return v
	}
	for _, k := range hide.top {
		delete(m, k)
	}
	if len(hide.children) > 0 {
		if list, ok := m["children"].([]any); ok {
			for _, c := range list {
				if cm, ok := c.(map[string]any); ok {
					for _, k := range hide.children {
						delete(cm, k)
					}
				}
			}
		}
	}
	return m
}
