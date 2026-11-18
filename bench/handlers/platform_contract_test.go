package handlers

// U1 UI area: the hosted platform's Go response types against the key lists
// the UI's TypeScript types are checked with (testdata/contract/*.keys, read by
// ui/lib/testing/platformFixtures.test.ts). A json tag without omitempty is a
// required key, one with omitempty an optional key ("?" in the file), so a
// field added, renamed or made optional on either side fails here.

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// jsonKeys lists a struct type's JSON keys, split into required and
// optional (omitempty), following embedded structs as encoding/json does.
func jsonKeys(t reflect.Type) (required, optional []string) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			r, o := jsonKeys(ft)
			required, optional = append(required, r...), append(optional, o...)
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if slices.Contains(strings.Split(opts, ","), "omitempty") {
			optional = append(optional, name)
		} else {
			required = append(required, name)
		}
	}
	return required, optional
}

func TestPlatformTypesMatchContract(t *testing.T) {
	cases := []struct {
		keys string
		v    any
	}{
		{"Job", jobs.Job{}},
		{"JobList", JobList{}},
		{"JobLogs", jobs.LogPage{}},
		{"JobLogLine", jobs.LogLine{}},
		{"AuthMe", auth.Me{}},
		{"AuthConfig", auth.AuthInfo{}},
		{"MeOrg", auth.MeOrg{}},
		{"PlatformUser", auth.User{}},
		{"InvitationPreview", auth.InvitationInfo{}},
		{"ApiToken", auth.APIToken{}},
		{"ApiTokenList", TokenList{}},
		{"ApiTokenCreated", auth.TokenCreated{}},
		{"Member", MemberInfo{}},
		{"MemberList", MemberList{}},
		{"Invitation", InvitationInfo{}},
		{"InvitationList", InvitationList{}},
		{"OrgInfo", OrgInfo{}},
		{"OrgList", OrgList{}},
		{"AuditEntry", AuditRow{}},
		{"AuditList", AuditList{}},
		{"Runner", schemas.RunnerView{}},
		{"RunnerList", schemas.RunnersList{}},
		{"RunnerToken", schemas.RunnerTokenView{}},
		{"RunnerTokenList", schemas.RunnerTokensList{}},
		{"RunnerTokenCreated", schemas.RunnerTokenCreated{}},
	}
	for _, c := range cases {
		t.Run(c.keys, func(t *testing.T) {
			want := loadKeys(t, c.keys)
			required, optional := jsonKeys(reflect.TypeOf(c.v))
			slices.Sort(required)
			slices.Sort(optional)
			wantRequired, wantOptional := slices.Sorted(slices.Values(want.required)), slices.Sorted(slices.Values(want.optional))
			if !slices.Equal(required, wantRequired) {
				t.Errorf("required keys: Go %v, contract %v", required, wantRequired)
			}
			if !slices.Equal(optional, wantOptional) {
				t.Errorf("optional keys: Go %v, contract %v", optional, wantOptional)
			}
		})
	}
}
