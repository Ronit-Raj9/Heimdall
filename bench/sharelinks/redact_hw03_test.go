package sharelinks

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedactHidesPathsAndPeople (N293 step 8): worker paths, virtual keys,
// people and worker names never reach a shared page; display names stay.
func TestRedactHidesPathsAndPeople(t *testing.T) {
	body := []byte(`{"run":{"evidence":{"stdout_path":"/data/heimdall-runner/runs/x/agent.stream.jsonl","virtual_key_id":"vk_1"},
		"uploaded_by":"a@b.c","requested_by":"usr_1","updated_by":"usr_2","started_by":"usr_3","approved_by":"usr_4",
		"reviewed_by":"usr_5","labeller":"usr_6","author":"usr_7","email":"x@y.z","worker":"shared-runner-org_abc",
		"worker_id":"shared-runner-org_abc","host":"box-1","by":"usr_8","started_by_name":"Ada","score":0.5}}`)
	out, _ := json.Marshal(RedactJSON(body, false))
	s := string(out)
	for _, leak := range []string{"/data/heimdall-runner", "vk_1", "a@b.c", "usr_", "x@y.z", "org_abc", "box-1"} {
		if strings.Contains(s, leak) {
			t.Errorf("shared payload keeps %q: %s", leak, s)
		}
	}
	if !strings.Contains(s, `"started_by_name":"Ada"`) || !strings.Contains(s, `"score":0.5`) {
		t.Fatalf("display name or data removed: %s", s)
	}
}
