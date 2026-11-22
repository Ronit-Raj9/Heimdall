package annotate

import (
	"math"
	"sort"
)

// Agreement is how far the people who labelled a queue agree with each
// other and with the judge.
type Agreement struct {
	// Items with at least two human labels, and of them those where every
	// label is the same.
	MultiLabelled int `json:"multi_labelled"`
	Unanimous     int `json:"unanimous"`
	// PercentAgreement is the share of agreeing pairs of labels over the
	// multi-labelled items; nil when there is none.
	PercentAgreement *float64 `json:"percent_agreement"`
	// Cohen is Cohen's kappa for each pair of labellers who labelled the
	// same items (at least MinPairItems of them), and CohenMean their mean.
	Cohen     []PairKappa `json:"cohen"`
	CohenMean *float64    `json:"cohen_mean"`
	// Fleiss is Fleiss' kappa over the items with exactly K labels (K is
	// the queue's reviewers per item); nil with fewer than two such items
	// or when every label is the same class.
	Fleiss      *float64 `json:"fleiss"`
	FleissItems int      `json:"fleiss_items"`
	// Interpretation reads Fleiss (else CohenMean) on the Landis and Koch
	// scale: poor, slight, fair, moderate, substantial, almost perfect.
	Interpretation string `json:"interpretation,omitempty"`
	// Judge compares the judge's verdict with the resolved human label on
	// items that have both.
	Judge *JudgeAgreement `json:"judge,omitempty"`
}

// PairKappa is Cohen's kappa of two labellers over the items both labelled.
type PairKappa struct {
	A     string   `json:"a"`
	B     string   `json:"b"`
	Items int      `json:"items"`
	Kappa *float64 `json:"kappa"`
	Agree int      `json:"agree"`
}

// JudgeAgreement is the judge against the resolved human labels.
type JudgeAgreement struct {
	Items int      `json:"items"`
	Agree int      `json:"agree"`
	Rate  *float64 `json:"rate"`
	Kappa *float64 `json:"kappa"`
	// FalsePasses are items the judge passed that people failed.
	FalsePasses int `json:"false_passes"`
	FalseFails  int `json:"false_fails"`
}

// MinPairItems is the fewest shared items a Cohen pair needs.
const MinPairItems = 3

func f(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	r := math.Round(v*1000) / 1000
	return &r
}

// cohen is Cohen's kappa of two label lists of equal length over the
// classes correct and incorrect.
func cohen(a, b []string) *float64 {
	n := float64(len(a))
	if n == 0 {
		return nil
	}
	agree := 0.0
	ca := map[string]float64{}
	cb := map[string]float64{}
	for i := range a {
		if a[i] == b[i] {
			agree++
		}
		ca[a[i]]++
		cb[b[i]]++
	}
	po := agree / n
	pe := 0.0
	for _, c := range Labels() {
		pe += (ca[c] / n) * (cb[c] / n)
	}
	if pe == 1 {
		if po == 1 {
			one := 1.0
			return &one // both always gave the same single class: perfect agreement
		}
		return nil
	}
	return f((po - pe) / (1 - pe))
}

// fleiss is Fleiss' kappa of items that each have the same number of
// ratings (n >= 2): counts[i][j] is how many raters put item i in class j.
func fleiss(counts [][]int) *float64 {
	N := len(counts)
	if N < 2 {
		return nil
	}
	n := 0
	for _, c := range counts[0] {
		n += c
	}
	if n < 2 {
		return nil
	}
	k := len(counts[0])
	pj := make([]float64, k)
	pBar := 0.0
	for _, row := range counts {
		s := 0.0
		for j, c := range row {
			pj[j] += float64(c)
			s += float64(c * c)
		}
		pBar += (s - float64(n)) / float64(n*(n-1))
	}
	pBar /= float64(N)
	pe := 0.0
	for j := range pj {
		p := pj[j] / float64(N*n)
		pe += p * p
	}
	if pe == 1 {
		return nil
	}
	return f((pBar - pe) / (1 - pe))
}

// Interpret reads a kappa on the Landis and Koch scale.
func Interpret(k *float64) string {
	if k == nil {
		return ""
	}
	switch v := *k; {
	case v < 0:
		return "poor"
	case v <= 0.2:
		return "slight"
	case v <= 0.4:
		return "fair"
	case v <= 0.6:
		return "moderate"
	case v <= 0.8:
		return "substantial"
	default:
		return "almost perfect"
	}
}

// ComputeAgreement measures agreement over a queue's items; k is the
// reviewers per item.
func ComputeAgreement(items []Item, k int) Agreement {
	var out Agreement
	pairsAgree, pairs := 0, 0
	type pairKey struct{ a, b string }
	pairLabels := map[pairKey][2][]string{}
	var fleissCounts [][]int
	classIdx := map[string]int{}
	for i, c := range Labels() {
		classIdx[c] = i
	}
	jd := &JudgeAgreement{}
	var judgeA, judgeB []string
	for _, it := range items {
		ls := it.Labels
		if len(ls) >= 2 {
			out.MultiLabelled++
			same := true
			for i := 0; i < len(ls); i++ {
				for j := i + 1; j < len(ls); j++ {
					pairs++
					if ls[i].Label == ls[j].Label {
						pairsAgree++
					} else {
						same = false
					}
					a, b := ls[i], ls[j]
					if a.UserID > b.UserID {
						a, b = b, a
					}
					pk := pairKey{a.UserID, b.UserID}
					v := pairLabels[pk]
					v[0] = append(v[0], a.Label)
					v[1] = append(v[1], b.Label)
					pairLabels[pk] = v
				}
			}
			if same {
				out.Unanimous++
			}
			if len(ls) == k {
				row := make([]int, len(Labels()))
				for _, l := range ls {
					row[classIdx[l.Label]]++
				}
				fleissCounts = append(fleissCounts, row)
			}
		}
		if r := it.Resolved(); r != "" && it.JudgeLabel != "" {
			jd.Items++
			judgeA = append(judgeA, it.JudgeLabel)
			judgeB = append(judgeB, r)
			if it.JudgeLabel == r {
				jd.Agree++
			} else if it.JudgeLabel == LabelCorrect {
				jd.FalsePasses++
			} else {
				jd.FalseFails++
			}
		}
	}
	if pairs > 0 {
		out.PercentAgreement = f(float64(pairsAgree) / float64(pairs))
	}
	keys := make([]pairKey, 0, len(pairLabels))
	for pk := range pairLabels {
		keys = append(keys, pk)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	sum, cnt := 0.0, 0
	for _, pk := range keys {
		v := pairLabels[pk]
		pk2 := PairKappa{A: pk.a, B: pk.b, Items: len(v[0])}
		for i := range v[0] {
			if v[0][i] == v[1][i] {
				pk2.Agree++
			}
		}
		if pk2.Items >= MinPairItems {
			pk2.Kappa = cohen(v[0], v[1])
			if pk2.Kappa != nil {
				sum += *pk2.Kappa
				cnt++
			}
		}
		out.Cohen = append(out.Cohen, pk2)
	}
	if out.Cohen == nil {
		out.Cohen = []PairKappa{}
	}
	if cnt > 0 {
		out.CohenMean = f(sum / float64(cnt))
	}
	out.FleissItems = len(fleissCounts)
	if k >= 2 {
		out.Fleiss = fleiss(fleissCounts)
	}
	if out.Fleiss != nil {
		out.Interpretation = Interpret(out.Fleiss)
	} else {
		out.Interpretation = Interpret(out.CohenMean)
	}
	if jd.Items > 0 {
		jd.Rate = f(float64(jd.Agree) / float64(jd.Items))
		jd.Kappa = cohen(judgeA, judgeB)
		out.Judge = jd
	}
	return out
}
