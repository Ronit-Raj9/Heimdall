package handlers

import "strings"

// plainErr is an API error's text without the internal sentinel prefixes
// ("bad quickrun request: ", "invalid run control: "), so a person reads only
// the sentence (fix wave 3, N211 and N285). statusOf still reads the wrapped
// error, so status codes do not change.
func plainErr(err error) string {
	s := err.Error()
	for _, p := range []string{errQuickBad.Error() + ": ", "invalid run control: "} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}
