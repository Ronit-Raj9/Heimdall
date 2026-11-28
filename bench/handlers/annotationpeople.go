package handlers

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/annotate"
	"github.com/valyala/fasthttp"
)

// People in the labelling answers (fix wave 2, D4, the G18 follow-up):
// labellers, deciders and the queue's creator are shown by display name
// (displayActor). Emails stay in the answer only for an org admin (or the
// local single-user principal), who sees them as a tooltip.

// AnnotationLabelView is one label as the caller sees it: Name is the
// labeller's display name; email is left out for a caller who is not an
// org admin.
type AnnotationLabelView struct {
	annotate.Label
	Name string `json:"name,omitempty"`
}

// AnnotationResolutionView is an item's final label as the caller sees it:
// Name is the decider's display name; email is left out for a caller who is
// not an org admin.
type AnnotationResolutionView struct {
	annotate.Resolution
	Name string `json:"name,omitempty"`
}

// annotationPeople is the member names and the caller's right to raw
// emails, looked up once per request.
type annotationPeople struct {
	ctx   context.Context
	names map[string]string
}

const annotationPeopleKey = "annotationPeople"

// annotationPeopleOf returns the request's people lookup, made on first use.
func annotationPeopleOf(ctx *fasthttp.RequestCtx) *annotationPeople {
	if pp, ok := ctx.UserValue(annotationPeopleKey).(*annotationPeople); ok {
		return pp
	}
	pp := &annotationPeople{ctx: ctx, names: orgMemberNames(ctx)}
	ctx.SetUserValue(annotationPeopleKey, pp)
	return pp
}

// person is how a person (user id and email, either may be empty) is shown:
// the display name, and the email for an admin caller ("" otherwise).
func (pp *annotationPeople) person(userID, email string) (name, rawEmail string) {
	name, keepRaw := displayActor(pp.ctx, userID, pp.names)
	if email != "" && (userID == "" || name == strings.TrimSpace(userID)) {
		// Not a member by user id: try the email.
		if n, _ := displayActor(pp.ctx, email, pp.names); n != "" {
			name = n
		}
	}
	if strings.Contains(name, "@") {
		name, _, _ = strings.Cut(name, "@")
	}
	if keepRaw {
		rawEmail = email
	}
	return name, rawEmail
}

// nameItem fills the display names of an item view's labels and resolution
// and leaves out their emails for a caller who is not an org admin.
func (pp *annotationPeople) nameItem(v *AnnotationItemView) {
	for i := range v.Labels {
		l := &v.Labels[i]
		l.Name, l.Email = pp.person(l.UserID, l.Email)
	}
	if r := v.Resolution; r != nil && (r.UserID != "" || r.Email != "") {
		r.Name, r.Email = pp.person(r.UserID, r.Email)
	}
}

// itemViewFor is itemView with the people named for the request's caller.
func itemViewFor(ctx *fasthttp.RequestCtx, c caller, q *annotate.Queue, it *annotate.Item, manage bool) AnnotationItemView {
	v := itemView(c, q, it, manage)
	annotationPeopleOf(ctx).nameItem(&v)
	return v
}

// queueViewFor is queueView with the creator's email left out for a caller
// who is not an org admin, and the creator's display name in
// created_by_name.
func (h *annotationHandler) queueViewFor(ctx *fasthttp.RequestCtx, c caller, f *annotate.File) AnnotationQueueView {
	v := h.queueView(c, f)
	if v.CreatedBy != "" || v.CreatedByEmail != "" {
		pp := annotationPeopleOf(ctx)
		v.CreatedByName, v.CreatedByEmail = pp.person(v.CreatedBy, v.CreatedByEmail)
		if _, keepRaw := displayActor(ctx, "", nil); !keepRaw && strings.Contains(v.CreatedBy, "@") {
			v.Queue.CreatedBy = ""
		}
	}
	return v
}
