package handlers

import (
	"strings"
	"testing"
)

// N33: the labelling queue answers never carry the creator's email, not
// even to an org admin. People are shown by name.
func TestQueueNeverCarriesEmail(t *testing.T) {
	r := annRouter(t, nil)
	id := annQueue(t, r, AnnotationQueueRequest{Name: annName("Queue one")})
	p := APIPrefix + "/annotation/queues/" + id
	code, body := abCallAs(r, annAdmin, "GET", p+"/items", nil)
	if code != 200 {
		t.Fatalf("items: %d %s", code, body)
	}
	if strings.Contains(string(body), "created_by_email") || strings.Contains(string(body), "ada@example.com") {
		t.Fatalf("admin queue answer carries the creator's email: %s", body)
	}
	code, body = abCallAs(r, annAdmin, "POST", APIPrefix+"/annotation/queues", AnnotationQueueRequest{Name: annName("Queue two")})
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	if strings.Contains(string(body), "created_by_email") {
		t.Fatalf("create answer carries the creator's email: %s", body)
	}
}
