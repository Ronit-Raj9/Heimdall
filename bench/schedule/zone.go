package schedule

import (
	"strings"
	"time"
	// The server binary may run in a container with no zone files; embed
	// them so every IANA name loads whatever the image ships.
	_ "time/tzdata"
)

// oldZoneNames maps well known old IANA names that browsers still report
// (Intl gives Asia/Calcutta on many systems) to their current name. Go's
// embedded data keeps most links, but a container's zoneinfo may not, and the
// saved schedule should carry the current name either way.
var oldZoneNames = map[string]string{
	"Asia/Calcutta":        "Asia/Kolkata",
	"Asia/Saigon":          "Asia/Ho_Chi_Minh",
	"Asia/Katmandu":        "Asia/Kathmandu",
	"Asia/Rangoon":         "Asia/Yangon",
	"Asia/Dacca":           "Asia/Dhaka",
	"Asia/Thimbu":          "Asia/Thimphu",
	"Asia/Ulan_Bator":      "Asia/Ulaanbaatar",
	"Asia/Macao":           "Asia/Macau",
	"Asia/Ujung_Pandang":   "Asia/Makassar",
	"Europe/Kiev":          "Europe/Kyiv",
	"America/Buenos_Aires": "America/Argentina/Buenos_Aires",
	"America/Indianapolis": "America/Indiana/Indianapolis",
	"America/Louisville":   "America/Kentucky/Louisville",
	"America/Godthab":      "America/Nuuk",
	"Atlantic/Faeroe":      "Atlantic/Faroe",
	"Pacific/Truk":         "Pacific/Chuuk",
	"Pacific/Ponape":       "Pacific/Pohnpei",
	"Pacific/Enderbury":    "Pacific/Kanton",
	"Africa/Asmera":        "Africa/Asmara",
	"US/Eastern":           "America/New_York",
	"US/Central":           "America/Chicago",
	"US/Mountain":          "America/Denver",
	"US/Pacific":           "America/Los_Angeles",
	"Etc/UTC":              "UTC",
	"Etc/GMT":              "UTC",
	"GMT":                  "UTC",
	"Universal":            "UTC",
	"Zulu":                 "UTC",
}

// CanonicalZone returns the current name for a zone name, mapping well known
// old names; other names come back trimmed and unchanged.
func CanonicalZone(name string) string {
	name = strings.TrimSpace(name)
	if cur, ok := oldZoneNames[name]; ok {
		return cur
	}
	return name
}

// LoadZone loads a zone by name, accepting well known old names.
func LoadZone(name string) (*time.Location, error) {
	return time.LoadLocation(CanonicalZone(name))
}
