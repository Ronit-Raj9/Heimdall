package handlers

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/annotate"
	"github.com/maximhq/bifrost/bench/schemas"
)

// POST .../next takes an optional {"skip": [...]} body: with no body the
// reserved item comes back, with the reserved item skipped the other open
// item does, and a body that is not that object is refused (wave 4, p12-3).
func TestAnnotationNextSkipBody(t *testing.T) {
	root := t.TempDir()
	h := &annotationHandler{env: OrgEnv{Mode: schemas.Mode("live")}, root: root, now: time.Now}
	r := router.New()
	p := APIPrefix + "/annotation/queues"
	r.POST(p, h.create)
	r.POST(p+"/{id}/next", h.next)
	id := annQueue(t, r, AnnotationQueueRequest{Name: annName("q")})
	var its []annotate.Item
	for i := range 2 {
		its = append(its, annotate.Item{Task: fmt.Sprintf("t%d", i), Answer: "a", RunID: fmt.Sprintf("r%d", i), JudgeLabel: annotate.LabelCorrect})
	}
	if _, _, err := annotate.Open(root).AddItems(id, its, ""); err != nil {
		t.Fatal(err)
	}
	next := func(body any) (int, AnnotationNext) {
		code, out := abCallAs(r, annAdmin, "POST", p+"/"+id+"/next", body)
		var n AnnotationNext
		_ = json.Unmarshal(out, &n)
		return code, n
	}
	code, first := next(nil)
	if code != 200 || first.Item == nil {
		t.Fatalf("next: %d %+v", code, first)
	}
	if code, again := next(nil); code != 200 || again.Item == nil || again.Item.ID != first.Item.ID {
		t.Fatalf("next without a body must keep the reserved item: %d %+v", code, again.Item)
	}
	code, other := next(map[string]any{"skip": []string{first.Item.ID}})
	if code != 200 || other.Item == nil || other.Item.ID == first.Item.ID {
		t.Fatalf("skip: %d %+v", code, other.Item)
	}
	if code, _ := next(map[string]any{"skip": "nope"}); code != 400 {
		t.Fatalf("a bad body must be refused, got %d", code)
	}
}
