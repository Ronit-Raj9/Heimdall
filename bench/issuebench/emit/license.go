package emit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// LicenseUnknown is the licence of a repository whose licence file is
// missing or not recognised.
const LicenseUnknown = "unknown"

var licenseFiles = []string{"LICENSE", "LICENSE.txt", "LICENSE.md", "LICENSE.rst", "LICENCE", "LICENCE.txt", "LICENCE.md", "COPYING", "COPYING.txt"}

// licenseRules map licence text to an SPDX id; the first match wins, so
// the more specific texts come first.
var licenseRules = []struct {
	id string
	re *regexp.Regexp
}{
	{"AGPL-3.0", regexp.MustCompile(`(?i)GNU AFFERO GENERAL PUBLIC LICENSE`)},
	{"LGPL-3.0", regexp.MustCompile(`(?i)GNU LESSER GENERAL PUBLIC LICENSE\s+Version 3`)},
	{"LGPL-2.1", regexp.MustCompile(`(?i)GNU LESSER GENERAL PUBLIC LICENSE\s+Version 2\.1`)},
	{"GPL-3.0", regexp.MustCompile(`(?i)GNU GENERAL PUBLIC LICENSE\s+Version 3`)},
	{"GPL-2.0", regexp.MustCompile(`(?i)GNU GENERAL PUBLIC LICENSE\s+Version 2`)},
	{"Apache-2.0", regexp.MustCompile(`(?i)Apache License,?\s+Version 2\.0`)},
	{"MPL-2.0", regexp.MustCompile(`(?i)Mozilla Public License,?\s+(Version|v\.)\s*2\.0`)},
	{"BSD-3-Clause", regexp.MustCompile(`(?is)Redistribution and use in source and binary forms.*Neither the name`)},
	{"BSD-2-Clause", regexp.MustCompile(`(?is)Redistribution and use in source and binary forms`)},
	{"ISC", regexp.MustCompile(`(?i)Permission to use, copy, modify, and(/or)? distribute this software for any purpose`)},
	{"MIT", regexp.MustCompile(`(?i)Permission is hereby granted, free of charge, to any person obtaining a copy`)},
	{"Unlicense", regexp.MustCompile(`(?i)This is free and unencumbered software released into the public domain`)},
}

// DetectLicense reads the repository's licence file and returns its SPDX
// id, or LicenseUnknown.
func DetectLicense(repoDir string) string {
	for _, f := range licenseFiles {
		b, err := os.ReadFile(filepath.Join(repoDir, f))
		if err != nil {
			continue
		}
		text := string(b)
		if len(text) > 64<<10 {
			text = text[:64<<10]
		}
		for _, r := range licenseRules {
			if r.re.MatchString(text) {
				return r.id
			}
		}
		if s := strings.TrimSpace(text); s != "" {
			return LicenseUnknown
		}
	}
	return LicenseUnknown
}
