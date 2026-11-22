package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/annotate"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// annRouter serves the annotation routes over a temporary project folder
// and the given run index.
func annRouter(t *testing.T, index ReadIndex) *router.Router {
	t.Helper()
	h := &annotationHandler{env: OrgEnv{Mode: schemas.Mode("live"), Services: Services{Index: index}}, root: t.TempDir(), now: time.Now}
	r := router.New()
	r.GET(APIPrefix+"/annotation/people", h.people)
	p := APIPrefix + "/annotation/queues"
	r.POST(p, h.create)
	r.PUT(p+"/{id}", h.update)
	r.POST(p+"/{id}/items", h.addItems)
	r.GET(p+"/{id}/items", h.items)
	return r
}

var annAdmin = &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleAdmin, Email: "ada@example.com"}

// annQueue creates a queue as annAdmin and returns its id.
func annQueue(t *testing.T, r *router.Router, body AnnotationQueueRequest) string {
	t.Helper()
	code, out := abCallAs(r, annAdmin, "POST", APIPrefix+"/annotation/queues", body)
	if code != 201 {
		t.Fatalf("create queue: %d %s", code, out)
	}
	var q AnnotationQueueView
	if err := json.Unmarshal(out, &q); err != nil {
		t.Fatal(err)
	}
	return q.ID
}

func annName(s string) *string { return &s }

func installMembers(t *testing.T, names map[string]string) {
	t.Helper()
	SetMemberNames(func(context.Context, string) (map[string]string, error) { return names, nil })
	t.Cleanup(func() { SetMemberNames(nil) })
}

// N168: members read who can be named as labellers, by user id and display
// name, sorted by name; a server with no members list says it is local.
func TestAnnotationPeopleListsMembers(t *testing.T) {
	r := annRouter(t, nil)
	member := &auth.Principal{Kind: auth.KindUser, UserID: "usr_2", OrgID: "org_a", Role: auth.RoleMember, Email: "bo@example.com"}
	code, body := abCallAs(r, member, "GET", APIPrefix+"/annotation/people", nil)
	if code != 200 || !strings.Contains(string(body), `"local":true`) || !strings.Contains(string(body), `"people":[]`) {
		t.Fatalf("local: %d %s", code, body)
	}
	installMembers(t, map[string]string{
		"usr_1": "Zed", "zed@example.com": "Zed",
		"usr_2": "bo", "bo@example.com": "bo",
		"usr_3": "Ada Lovelace", "ada@example.com": "Ada Lovelace",
	})
	code, body = abCallAs(r, member, "GET", APIPrefix+"/annotation/people", nil)
	if code != 200 {
		t.Fatalf("people: %d %s", code, body)
	}
	var out AnnotationPeople
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	want := []AnnotationPerson{{UserID: "usr_3", Name: "Ada Lovelace"}, {UserID: "usr_2", Name: "bo"}, {UserID: "usr_1", Name: "Zed"}}
	if out.Local || len(out.People) != len(want) {
		t.Fatalf("people %+v", out)
	}
	for i := range want {
		if out.People[i] != want[i] {
			t.Fatalf("people[%d] = %+v, want %+v (all %+v)", i, out.People[i], want[i], out.People)
		}
	}
	if strings.Contains(string(body), "@") {
		t.Fatalf("emails listed: %s", body)
	}
}

// N167: with the members list installed, a labeller must be a member of the
// organization (by user id); a typo is refused, never saved.
func TestQueueUpdateRefusesUnknownLabeller(t *testing.T) {
	r := annRouter(t, nil)
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	id := annQueue(t, r, AnnotationQueueRequest{Name: annName("Refunds")})
	path := APIPrefix + "/annotation/queues/" + id
	for _, bad := range []string{"not-a-user-id", "ada@example.com"} {
		code, body := abCallAs(r, annAdmin, "PUT", path, AnnotationQueueRequest{Reviewers: &[]string{bad}})
		if code != fasthttp.StatusUnprocessableEntity || !strings.Contains(string(body), bad+" is not a member of this organization. Pick labellers from the list.") {
			t.Fatalf("%s: %d %s", bad, code, body)
		}
	}
	if code, body := abCallAs(r, annAdmin, "PUT", path, AnnotationQueueRequest{Reviewers: &[]string{"usr_1"}}); code != 200 {
		t.Fatalf("member: %d %s", code, body)
	}
	code, body := abCallAs(r, annAdmin, "POST", APIPrefix+"/annotation/queues", AnnotationQueueRequest{Name: annName("x"), Reviewers: &[]string{"usr_9"}})
	if code != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("create with unknown labeller: %d %s", code, body)
	}
}

// N167: people per item outside 1 to 5 is refused when it is sent, not
// turned into 1; leaving it out keeps the default.
func TestQueueUpdateRefusesZeroPeoplePerItem(t *testing.T) {
	r := annRouter(t, nil)
	id := annQueue(t, r, AnnotationQueueRequest{Name: annName("Refunds")})
	path := APIPrefix + "/annotation/queues/" + id
	for _, n := range []int{0, -1, 6} {
		n := n
		code, body := abCallAs(r, annAdmin, "PUT", path, AnnotationQueueRequest{ReviewersPerItem: &n})
		if code != fasthttp.StatusUnprocessableEntity || !strings.Contains(string(body), "People per item is 1 to 5.") {
			t.Fatalf("%d: %d %s", n, code, body)
		}
		code, body = abCallAs(r, annAdmin, "POST", APIPrefix+"/annotation/queues", AnnotationQueueRequest{Name: annName("y"), ReviewersPerItem: &n})
		if code != fasthttp.StatusUnprocessableEntity {
			t.Fatalf("create %d: %d %s", n, code, body)
		}
	}
	three := 3
	code, body := abCallAs(r, annAdmin, "PUT", path, AnnotationQueueRequest{ReviewersPerItem: &three})
	var q AnnotationQueueView
	json.Unmarshal(body, &q)
	if code != 200 || q.ReviewersPerItem != 3 {
		t.Fatalf("3: %d %s", code, body)
	}
}

// annRunIndex is one experiment with one sealed, counted run whose files are
// the given ones.
func annRunIndex(files map[string][]byte, claim *schemas.Claim) *fakeIndex {
	run := schemas.Run{RunID: "r_1", ExpID: "e_1", Claim: claim}
	return &fakeIndex{
		manifests: map[string]*schemas.Manifest{"e_1": {ExpID: "e_1", SuiteID: "bfcl"}},
		runs:      []RunView{{Slot: schemas.Slot{ExpID: "e_1", Task: "t1", Arm: "A", Counted: true}, Run: run, Sealed: true}},
		files:     map[string]map[string][]byte{"r_1": files},
	}
}

// annAddRuns adds the runs of e_1 to a new queue and returns the result and
// the queue's items as its owner sees them.
func annAddRuns(t *testing.T, ix *fakeIndex) (AnnotationItemsResult, []AnnotationItemView) {
	t.Helper()
	r := annRouter(t, ix)
	id := annQueue(t, r, AnnotationQueueRequest{Name: annName("BFCL")})
	code, body := abCallAs(r, annAdmin, "POST", APIPrefix+"/annotation/queues/"+id+"/items", AnnotationItemsRequest{Experiment: "e_1"})
	if code != 201 {
		t.Fatalf("add: %d %s", code, body)
	}
	var res AnnotationItemsResult
	json.Unmarshal(body, &res)
	code, body = abCallAs(r, annAdmin, "GET", APIPrefix+"/annotation/queues/"+id+"/items", nil)
	if code != 200 {
		t.Fatalf("items: %d %s", code, body)
	}
	var list AnnotationItemList
	json.Unmarshal(body, &list)
	return res, list.Items
}

const annDiff = "diff --git a/result.json b/result.json\nnew file mode 100644\nindex 0000000..1111111\n--- /dev/null\n+++ b/result.json\n@@ -0,0 +1,2 @@\n+[{\"calculate_bmi\": {\"weight\": 70}},\n+ {\"calculate_bmi\": {\"weight\": 80}}]\n"

// N72: a run with no answer file is labelled from the files the agent wrote
// (its workspace diff), and the item says so.
func TestRunItemsFallsBackToWorkspaceDiff(t *testing.T) {
	res, items := annAddRuns(t, annRunIndex(map[string][]byte{FileDiff: []byte(annDiff)}, &schemas.Claim{Text: "done"}))
	if res.Added != 1 || len(res.Skipped) != 0 || len(items) != 1 {
		t.Fatalf("result %+v items %+v", res, items)
	}
	it := items[0]
	if it.AnswerFrom != annotate.AnswerFromFiles || it.Answer != "result.json:\n[{\"calculate_bmi\": {\"weight\": 70}},\n {\"calculate_bmi\": {\"weight\": 80}}]\n" {
		t.Fatalf("item from %q answer %q", it.AnswerFrom, it.Answer)
	}

	// The answer file still wins when there is one.
	_, items = annAddRuns(t, annRunIndex(map[string][]byte{graders.AnswerFile: []byte("42"), FileDiff: []byte(annDiff)}, nil))
	if len(items) != 1 || items[0].Answer != "42" || items[0].AnswerFrom != annotate.AnswerFromFile {
		t.Fatalf("answer file: %+v", items)
	}
}

// N72: with no answer file and no changed files, the agent's final message
// is labelled; with none of the three the run is skipped and the reason
// names all three.
func TestRunItemsFallsBackToFinalMessage(t *testing.T) {
	res, items := annAddRuns(t, annRunIndex(map[string][]byte{FileDiff: []byte("  \n")}, &schemas.Claim{Text: "The BMI is 22.9."}))
	if res.Added != 1 || len(items) != 1 || items[0].Answer != "The BMI is 22.9." || items[0].AnswerFrom != annotate.AnswerFromFinalMessage {
		t.Fatalf("result %+v items %+v", res, items)
	}
	res, _ = annAddRuns(t, annRunIndex(map[string][]byte{}, &schemas.Claim{Text: " "}))
	if res.Added != 0 || len(res.Skipped) != 1 || res.Skipped[0] != "run r_1: the run saved no answer, changed no files and sent no final message" {
		t.Fatalf("skipped %+v", res)
	}
}
