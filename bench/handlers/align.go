package handlers

import "github.com/maximhq/bifrost/bench/schemas"

// maxAlignCells caps the LCS table after the common prefix and suffix are
// trimmed: 4M int32 cells is 16 MB. Larger middles are listed unaligned.
const maxAlignCells = 4_000_000

// DivergenceNote labels the first divergence: what was seen, not why.
const DivergenceNote = "first observed divergence: an observation, not a root cause"

// AlignedPosition is one aligned row as indexes into the two event lists; -1
// marks a lane with no event on that row.
type AlignedPosition struct {
	A, B    int
	Type    schemas.EventType
	Target  string
	Matched bool
}

// Alignment is two runs' events aligned on (event type, target).
type Alignment struct {
	Positions []AlignedPosition
	Matched   int
	// FirstDivergence is the index of the first unmatched row, or -1 when the
	// lanes match throughout.
	FirstDivergence int
	// Approximate is true when the lanes were too long to align fully and the
	// differing middle is listed without matching.
	Approximate bool
}

// Steps renders the alignment as API steps keyed by arm id, with the first
// divergence. a and b must be the lists AlignPair was given.
func (al Alignment) Steps(armA, armB string, a, b []schemas.Event) ([]schemas.AlignedStep, *schemas.FirstDivergence) {
	steps := make([]schemas.AlignedStep, len(al.Positions))
	for i, p := range al.Positions {
		steps[i] = schemas.AlignedStep{
			Index: i, Type: p.Type, Target: p.Target, Diverged: !p.Matched,
			Events: map[string]*string{armA: eventID(a, p.A), armB: eventID(b, p.B)},
		}
	}
	if al.FirstDivergence < 0 {
		return steps, nil
	}
	return steps, &schemas.FirstDivergence{Index: al.FirstDivergence, Note: DivergenceNote}
}

// AlignPair aligns two event lists with a longest common subsequence over
// (type, target). Where the lanes differ, A's unmatched events come before
// B's. It is pure: no I/O, and the inputs are not modified.
func AlignPair(a, b []schemas.Event) Alignment {
	ka, kb := alignKeys(a), alignKeys(b)
	n, m := len(a), len(b)
	pre := 0
	for pre < n && pre < m && ka[pre] == kb[pre] {
		pre++
	}
	suf := 0
	for suf < n-pre && suf < m-pre && ka[n-1-suf] == kb[m-1-suf] {
		suf++
	}
	out := Alignment{Positions: make([]AlignedPosition, 0, max(n, m)), FirstDivergence: -1}
	for i := 0; i < pre; i++ {
		out.add(a, b, i, i)
	}
	out.alignMiddle(a, b, ka, kb, pre, n-suf, pre, m-suf)
	for k := 0; k < suf; k++ {
		out.add(a, b, n-suf+k, m-suf+k)
	}
	return out
}

// alignMiddle aligns a[ia:na] with b[ib:nb], the part that differs.
func (al *Alignment) alignMiddle(a, b []schemas.Event, ka, kb []string, ia, na, ib, nb int) {
	rows, cols := na-ia, nb-ib
	if rows == 0 || cols == 0 || rows*cols > maxAlignCells {
		al.Approximate = rows > 0 && cols > 0
		for i := ia; i < na; i++ {
			al.add(a, b, i, -1)
		}
		for j := ib; j < nb; j++ {
			al.add(a, b, -1, j)
		}
		return
	}
	// lcs[i*(cols+1)+j] is the LCS length of a[ia+i:na] and b[ib+j:nb].
	w := cols + 1
	lcs := make([]int32, (rows+1)*w)
	for i := rows - 1; i >= 0; i-- {
		for j := cols - 1; j >= 0; j-- {
			if ka[ia+i] == kb[ib+j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < rows || j < cols {
		switch {
		case i < rows && j < cols && ka[ia+i] == kb[ib+j]:
			al.add(a, b, ia+i, ib+j)
			i++
			j++
		case j == cols || (i < rows && lcs[(i+1)*w+j] >= lcs[i*w+j+1]):
			al.add(a, b, ia+i, -1)
			i++
		default:
			al.add(a, b, -1, ib+j)
			j++
		}
	}
}

// add appends one row; -1 marks a missing side.
func (al *Alignment) add(a, b []schemas.Event, i, j int) {
	p := AlignedPosition{A: i, B: j, Matched: i >= 0 && j >= 0}
	if i >= 0 {
		p.Type, p.Target = a[i].Type, a[i].Target
	} else {
		p.Type, p.Target = b[j].Type, b[j].Target
	}
	if p.Matched {
		al.Matched++
	} else if al.FirstDivergence < 0 {
		al.FirstDivergence = len(al.Positions)
	}
	al.Positions = append(al.Positions, p)
}

// eventID returns the id of events[i], or nil for a missing side.
func eventID(events []schemas.Event, i int) *string {
	if i < 0 || i >= len(events) {
		return nil
	}
	id := events[i].EventID
	return &id
}

// alignKeys returns the (type, target) key of every event.
func alignKeys(events []schemas.Event) []string {
	keys := make([]string, len(events))
	for i, e := range events {
		keys[i] = string(e.Type) + "\x00" + e.Target
	}
	return keys
}
