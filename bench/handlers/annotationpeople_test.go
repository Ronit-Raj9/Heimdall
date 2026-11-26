package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/annotate"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
)

// annPeopleRouter serves the queue, item, label and agreement routes.
func annPeopleRouter(t *testing.T) *router.Router {
	t.Helper()
	h := &annotationHandler{env: OrgEnv{Mode: schemas.Mode("live")}, root: t.TempDir(), now: time.Now}
	r := router.New()
	p := APIPrefix + "/annotation/queues"
	r.GET(p, h.list)
	r.POST(p, h.create)
	r.GET(p+"/{id}", h.get)
	r.POST(p+"/{id}/items", h.addItems)
	r.GET(p+"/{id}/items", h.items)
	r.POST(p+"/{id}/items/{item}/labels", h.label)
	r.GET(p+"/{id}/agreement", h.agreement)
	return r
}

// The labelling Items table, the Agreement tab and the queue itself name
// people by display name; a member never gets an email, an admin does.
func TestAnnotationHidesLabellerEmailsFromMembers(t *testing.T) {
	installMembers(t, map[string]string{
		"usr_1": "Ada", "ada@example.com": "Ada",
		"usr_2": "bo", "bo@example.com": "bo",
	})
	r := annPeopleRouter(t)
	one := 1
	id := annQueue(t, r, AnnotationQueueRequest{Name: annName("Refunds"), ReviewersPerItem: &one})
	p := APIPrefix + "/annotation/queues/" + id
	if code, body := abCallAs(r, annAdmin, "POST", p+"/items", AnnotationItemsRequest{Items: []annotate.Item{{Task: "t1", Answer: "42"}}}); code != 201 {
		t.Fatalf("add: %d %s", code, body)
	}
	member := &auth.Principal{Kind: auth.KindUser, UserID: "usr_2", OrgID: "org_a", Role: auth.RoleMember, Email: "bo@example.com"}
	code, body := abCallAs(r, member, "GET", p+"/items", nil)
	if code != 200 {
		t.Fatalf("items: %d %s", code, body)
	}
	var list AnnotationItemList
	if err := json.Unmarshal(body, &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("items %s (%v)", body, err)
	}
	if code, body := abCallAs(r, member, "POST", p+"/items/"+list.Items[0].ID+"/labels", AnnotationLabelRequest{Label: "correct"}); code != 201 {
		t.Fatalf("label: %d %s", code, body)
	}

	for _, path := range []string{p, p + "/items", p + "/agreement", APIPrefix + "/annotation/queues"} {
		code, body := abCallAs(r, member, "GET", path, nil)
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		if strings.Contains(string(body), "@example.com") {
			t.Fatalf("%s shows an email to a member: %s", path, body)
		}
	}
	_, body = abCallAs(r, member, "GET", p+"/items", nil)
	list = AnnotationItemList{}
	json.Unmarshal(body, &list)
	it := list.Items[0]
	if len(it.Labels) != 1 || it.Labels[0].Name != "bo" || it.Labels[0].UserID != "usr_2" {
		t.Fatalf("labels %+v", it.Labels)
	}
	if list.Queue.CreatedByName != "Ada" {
		t.Fatalf("queue creator name %q", list.Queue.CreatedByName)
	}
	_, body = abCallAs(r, member, "GET", p+"/agreement", nil)
	var ag AnnotationAgreement
	json.Unmarshal(body, &ag)
	if ag.Labellers["usr_2"] != "bo" || ag.LabellerEmails != nil {
		t.Fatalf("agreement %s", body)
	}

	// An admin sees the same names and gets the emails for a tooltip.
	_, body = abCallAs(r, annAdmin, "GET", p+"/items", nil)
	list = AnnotationItemList{}
	json.Unmarshal(body, &list)
	if l := list.Items[0].Labels; len(l) != 1 || l[0].Name != "bo" || l[0].Email != "bo@example.com" {
		t.Fatalf("admin labels %+v", l)
	}
	// Wave 4, N33: the queue creator's email is never sent, not even to an admin.
	if list.Queue.NoCreatorEmail != "" || list.Queue.Queue.CreatedByEmail != "" {
		t.Fatalf("admin queue creator email %q", list.Queue.NoCreatorEmail)
	}
	_, body = abCallAs(r, annAdmin, "GET", p+"/agreement", nil)
	ag = AnnotationAgreement{}
	json.Unmarshal(body, &ag)
	if ag.Labellers["usr_2"] != "bo" || ag.LabellerEmails["usr_2"] != "bo@example.com" {
		t.Fatalf("admin agreement %s", body)
	}
}

// A workspace diff longer than one item holds is shortened, not skipped:
// the first part of the files with a line saying where the rest is.
func TestRunItemsShortensLongWorkspaceDiff(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/big.txt b/big.txt\nnew file mode 100644\n--- /dev/null\n+++ b/big.txt\n@@ -0,0 +1,4000 @@\n")
	for i := 0; i < 4000; i++ {
		b.WriteString("+line of the file the agent wrote, long enough to pass 64 KiB é\n")
	}
	res, items := annAddRuns(t, annRunIndex(map[string][]byte{FileDiff: []byte(b.String())}, nil))
	if res.Added != 1 || len(res.Skipped) != 0 || len(items) != 1 {
		t.Fatalf("result %+v items %d", res, len(items))
	}
	ans := items[0].Answer
	if items[0].AnswerFrom != annotate.AnswerFromFiles || !strings.HasPrefix(ans, "big.txt:\nline of the file") {
		t.Fatalf("from %q answer starts %q", items[0].AnswerFrom, ans[:40])
	}
	if !strings.HasSuffix(ans, "\n\nShortened: the full files are in the run.\n") || len(ans) > 64<<10 {
		t.Fatalf("answer length %d, ends %q", len(ans), ans[len(ans)-80:])
	}
	if !strings.HasSuffix(strings.TrimSuffix(ans, "\n\nShortened: the full files are in the run.\n"), "é") {
		t.Fatal("the shortened text does not end on a whole line")
	}
}
