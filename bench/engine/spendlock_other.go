//go:build !unix

package engine

// lockSpendFile has no interprocess lock off unix: the in-process mutex
// alone serializes updates, so run one runner per runs root there.
func lockSpendFile(string) (func(), error) { return func() {}, nil }
