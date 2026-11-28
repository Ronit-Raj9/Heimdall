package gwfeature

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// ufl1PromptBody is the exact body the Gateway page's Start a test form
// sends for the prompt version test after picking a prompt and two versions
// (bench/ui/app/gateway/views/promptPicker.ufl1.test.tsx asserts the same
// string).
const ufl1PromptBody = `{"feature":"prompt_version","suite":"s@1","base":"agents/a.yaml","trials":2,"budget_usd":5,"options":{"prompt_id":"pr-9","variant_version":"4","control_version":"3"}}`

func TestPromptDraftFromWebPickerBody(t *testing.T) {
	var req DraftRequest
	dec := json.NewDecoder(bytes.NewReader([]byte(ufl1PromptBody)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		t.Fatal(err)
	}
	f, ok := Lookup(req.Feature)
	if !ok {
		t.Fatal("feature prompt_version is not registered")
	}
	exp, _, err := f.Draft(context.Background(), DraftEnv{Root: t.TempDir(), SourceGet: promptSource(t)}, req)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Arms["A"].Label != "prompt v3" || exp.Arms["B"].Label != "prompt v4" {
		t.Fatalf("arms %+v", exp.Arms)
	}
}
