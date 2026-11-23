package handlers

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
)

// Fix wave 3, N221: a user id that is no longer a member of the
// organization reads "a former member"; only an admin still gets the id.
func TestActorNamesFormerMember(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	member := auth.WithPrincipal(context.Background(), actorMember)
	admin := auth.WithPrincipal(context.Background(), actorAdmin)
	if keep, name := newActorNames(member).show("usr_9"); keep != "" || name != "a former member" {
		t.Fatalf("member: show(usr_9) = %q, %q; want \"\", \"a former member\"", keep, name)
	}
	if keep, name := newActorNames(admin).show("usr_9"); keep != "usr_9" || name != "a former member" {
		t.Fatalf("admin: show(usr_9) = %q, %q; want usr_9, \"a former member\"", keep, name)
	}
	// A member still reads the name; a non-id actor is unchanged.
	if keep, name := newActorNames(member).show("usr_1"); keep != "usr_1" || name != "Ada" {
		t.Fatalf("member: show(usr_1) = %q, %q", keep, name)
	}
	if keep, name := newActorNames(member).show("schedule:nightly"); keep != "schedule:nightly" || name != "" {
		t.Fatalf("member: show(schedule:nightly) = %q, %q", keep, name)
	}
	// Without a members list (local mode) the id stays as it is.
	SetMemberNames(nil)
	if keep, name := newActorNames(member).show("usr_9"); keep != "usr_9" || name != "" {
		t.Fatalf("local: show(usr_9) = %q, %q", keep, name)
	}
}
