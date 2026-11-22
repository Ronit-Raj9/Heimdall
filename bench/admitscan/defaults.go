package admitscan

// DefaultOptions returns the options `skill scan` uses when no flag is given
// (cmd/bifrost-bench/admitscan.go), static only: Dynamic is false, so the
// scan reads files and starts no container. The server scans uploaded
// skills with these in process (fix wave 2, N22).
func DefaultOptions() Options {
	return Options{
		Dynamic:   false,
		Wall:      DefaultWall,
		Base64Min: DefaultBase64Min,
		HexMin:    DefaultHexMin,
	}
}
