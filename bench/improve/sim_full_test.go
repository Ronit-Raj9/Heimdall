//go:build improvesim

package improve

// The full mode of the simulation suite: 6,000 replications per cell and
// the full-mode bounds of A2.13.
func init() {
	simReps = 6000
	simFull = true
}
