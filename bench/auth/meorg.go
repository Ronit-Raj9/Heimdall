package auth

import "context"

// MeFor is Me for a tab that names its organization (ref, an org id or slug,
// from the X-Heimdall-Org header; wave 4, organization per tab). For a user
// with ref set: when ref is one of their orgs, the answer describes that org
// and DefaultOrg names the session's default; otherwise OrgReset is set and
// the session's org stays current. Tokens and runners ignore ref.
func (s *Service) MeFor(ctx context.Context, p *Principal, ref string) (*Me, error) {
	me, err := s.Me(ctx, p)
	if err != nil || me == nil || ref == "" || p == nil || p.Kind != KindUser {
		return me, err
	}
	for _, o := range me.Orgs {
		if o.ID == ref || o.Slug == ref {
			me.DefaultOrg = me.CurrentOrg
			me.CurrentOrg = o.ID
			me.Role = o.Role
			return me, nil
		}
	}
	me.OrgReset = true
	return me, nil
}
