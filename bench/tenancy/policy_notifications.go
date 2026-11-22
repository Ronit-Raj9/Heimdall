package tenancy

// Email notifications (handlers/notifications.go, lane Y05). Each route
// acts on the caller's own settings, so every member, viewers included,
// may read and change them: a viewer must be able to stop email to their
// own address.
func init() {
	for k, r := range map[string]Rule{
		key("GET", api+"/notifications/email"):       org(viewer),
		key("PUT", api+"/notifications/email"):       org(viewer),
		key("POST", api+"/notifications/email/test"): org(viewer),
	} {
		OrgPolicy[k] = r
	}
	for _, k := range []string{key("PUT", api+"/notifications/email"), key("POST", api+"/notifications/email/test")} {
		selfWrites[k] = true
	}
}

// selfWrites are org writes a viewer may make because they change only the
// caller's own settings, never org data. The policy contract test exempts
// them from "every write needs at least member".
var selfWrites = map[string]bool{}
