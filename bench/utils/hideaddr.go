package utils

import "net/http"

// AddrHiddenError is an error whose text has private addresses replaced by
// HidePrivateAddresses (fix wave 2, N104). A dial error names the address a
// host resolved to; wrapped in this, it can reach a member without telling
// them what an internal name resolves to. errors.Is and errors.As still see
// the original error.
type AddrHiddenError struct{ Err error }

func (e AddrHiddenError) Error() string { return HidePrivateAddresses(e.Err.Error()) }
func (e AddrHiddenError) Unwrap() error { return e.Err }

// HideAddresses wraps err in AddrHiddenError; nil stays nil.
func HideAddresses(err error) error {
	if err == nil {
		return nil
	}
	return AddrHiddenError{Err: err}
}

// HidingAddresses wraps an HTTP transport so every error it returns hides
// private addresses (AddrHiddenError). Responses pass through unchanged.
func HidingAddresses(next http.RoundTripper) http.RoundTripper {
	return addrHidingTransport{next: next}
}

type addrHidingTransport struct{ next http.RoundTripper }

func (t addrHidingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(r)
	if err != nil {
		return nil, HideAddresses(err)
	}
	return resp, nil
}
