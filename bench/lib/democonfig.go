package lib

// DemoConfig is the demo section (Tenancy area, docs/platform-design.md):
//
//	demo:
//	  org: demo
//
// Retired: it is still parsed so older config files load, but it has no
// effect. A hosted install requires sign-in for every organization and for
// the benchmark library; anonymous visitors see only the sign-in page, and
// an organization named here is neither resolved for them nor flagged as a
// demo (tenancy.Resolve).
type DemoConfig struct {
	Org string `yaml:"org" json:"org,omitempty"`
}
