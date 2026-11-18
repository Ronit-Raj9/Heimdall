package lib

// DemoConfig is the demo section (Tenancy area, docs/platform-design.md):
//
//	demo:
//	  org: demo   # slug of the public read-only organization, empty for none
//
// Anonymous visitors of a hosted server land in this organization when it
// exists and has public_read set; they can read it and nothing else. Empty
// (the default) means anonymous visitors see nothing but the sign-in page.
type DemoConfig struct {
	Org string `yaml:"org" json:"org,omitempty"`
}
