package verdict

// Verdict.Basis values (fix wave 3, N244): what decided a regressed or
// improved verdict of two setups.
const (
	// BasisEffect: the primary effect was significant at both missingness
	// bounds.
	BasisEffect = "effect"
	// BasisGuard: only a failed guard decided it (for example a veto gate
	// that fired in arm B where arm A has none); the effect alone was not
	// significant, so no page may say "B is worse by ...".
	BasisGuard = "guard"
)
