package improve

import "fmt"

// The verdict's words (A2.12): one headline per state and the lines under
// it, each only when it applies. Points carry a sign and no decimals above
// 10, one decimal below; p has two significant digits; money two decimals.

func init() { Wording = wordVerdict }

// wordVerdict sets v.Headline and v.Sentences from the claim's numbers. rec
// may be nil (the lines that need the run are then left out).
func wordVerdict(v *Verdict, rec *Record) {
	if v == nil {
		return
	}
	h := headline(v, rec)
	if rec != nil && rec.Extension != nil && rec.Final != nil && v.N == rec.Extension.N {
		f := rec.Final
		h = fmt.Sprintf("First look: %s points on %d held-out tasks (p %s). Second look, with %d tasks: %s",
			formatPoints(f.Gain), f.N, formatP(f.PUpper), v.N, h)
	}
	v.Headline = h
	v.Sentences = verdictLines(v, rec)
}

func versionNumber(rec *Record) int {
	if rec == nil {
		return 0
	}
	return rec.Incumbent.Number
}

func headline(v *Verdict, rec *Record) string {
	n, gain := v.N, formatPoints(v.Gain)
	lo80, hi80 := formatPoints(v.CI80[0]), formatPoints(v.CI80[1])
	lo95, hi95 := formatPoints(v.CI95[0]), formatPoints(v.CI95[1])
	p := formatP(v.PUpper)
	switch v.State {
	case VerdictSupported:
		return fmt.Sprintf("Version %d is better: %s points on %d held-out tasks it never saw (95%% interval %s to %s, p %s).",
			versionNumber(rec), gain, n, lo95, hi95, p)
	case VerdictLikelyBetter:
		return fmt.Sprintf("Likely better, not proven: %s points on %d held-out tasks (80%% interval %s to %s, p %s). %s",
			gain, n, lo80, hi80, p, mdeSentence(v.MDE))
	case VerdictInsufficientSample:
		return fmt.Sprintf("Not enough held-out tasks to tell: %d tasks, %d of them with different results for the two versions. "+
			"Even a clear gain could not be confirmed with this many. Observed: %s points.", n, v.NNonzero, gain)
	case VerdictNoEvidence:
		return fmt.Sprintf("No clear change on the held-out tasks: %s points on %d tasks (80%% interval %s to %s). "+
			"Keep version 0 unless you have other reasons.", gain, n, lo80, hi80)
	case VerdictWorse:
		return fmt.Sprintf("Version %d is worse: %s points on %d held-out tasks (95%% interval %s to %s, p %s). Keep version 0.",
			versionNumber(rec), gain, n, lo95, hi95, formatP(v.PLower))
	case VerdictNoChange:
		return "No new version was kept: none passed the gate. The agent stays at version 0."
	case VerdictNotClaimable:
		return fmt.Sprintf("A person approved a paused check in round %d, so this result cannot be claimed. Observed: %s points on %d held-out tasks.",
			approvedRound(rec), gain, n)
	}
	return v.Headline
}

// mdeSentence is the detectable gain; a stored 0 means fewer than 2 tasks.
func mdeSentence(mde float64) string {
	if mde <= 0 {
		return "There are not enough tasks to estimate which gains this many tasks can confirm."
	}
	return fmt.Sprintf("This many tasks can confirm gains of about %s points or more.", unsignedPoints(mde))
}

func unsignedPoints(x float64) string {
	s := formatPoints(x)
	if len(s) > 0 && s[0] == '+' {
		return s[1:]
	}
	return s
}

func approvedRound(rec *Record) int {
	if rec == nil {
		return 0
	}
	for _, rd := range rec.Rounds {
		if len(rd.Approvals) > 0 {
			return rd.N
		}
	}
	return 0
}

func money(x float64) string { return fmt.Sprintf("$%.2f", x) }

// verdictLines are the lines under the headline, in the A2.12 order.
func verdictLines(v *Verdict, rec *Record) []string {
	var out []string
	if c := v.Continue; c != nil {
		target := DefaultTargetGain
		if rec != nil && rec.Spec.Claim.TargetGain > 0 {
			target = rec.Spec.Claim.TargetGain
		}
		usd := c.USD[1]
		if c.ExtraTrials > 0 {
			out = append(out, fmt.Sprintf("To confirm a %s point gain: about %d more held-out tasks, or %d more tries on each task, about %s.",
				unsignedPoints(target), c.NewTasks, c.ExtraTrials, money(usd)))
		} else {
			out = append(out, fmt.Sprintf("To confirm a %s point gain: about %d more held-out tasks, about %s.",
				unsignedPoints(target), c.NewTasks, money(usd)))
		}
	}
	if nl := v.Null; nl != nil {
		s := fmt.Sprintf("Edits made without looking at failures (same rounds, same budget) scored %.0f%%. Version %d is %s points ahead of them (80%% interval %s to %s).",
			100*nl.Score, versionNumber(rec), formatPoints(nl.Diff), formatPoints(nl.CI80[0]), formatPoints(nl.CI80[1]))
		if nl.CI80[0] <= 0 {
			s += " So feedback is not shown to matter here."
		}
		out = append(out, s)
	}
	if rt := v.Retries; rt != nil {
		var loop, used float64
		k := 0
		if rec != nil && rec.Controls != nil && rec.Controls.Retries != nil {
			rc := rec.Controls.Retries
			loop, used, k = rc.LoopUSD, rc.UsedUSD, rc.K
		}
		out = append(out, fmt.Sprintf("Retrying version 0 when a run visibly failed, with up to the same money (%s, up to %d tries per task, %s used), scored %.0f%%. Version %d is %s points ahead (80%% interval %s to %s).",
			money(loop), k, money(used), 100*rt.Score, versionNumber(rec), formatPoints(rt.Diff), formatPoints(rt.CI80[0]), formatPoints(rt.CI80[1])))
	}
	if v.Teacher != "" {
		out = append(out, fmt.Sprintf("Teacher-assisted: the improver ran on %s, not on the agent's own model. Its cost is in the totals.", v.Teacher))
	}
	if rec != nil && committedRounds(rec) > 0 {
		alpha := rec.Spec.Commit.Alpha
		if alpha <= 0 {
			alpha = DefaultClaimAlpha
		}
		out = append(out, fmt.Sprintf("Each kept version passed a test on the gate tasks. Over the whole run, the chance that any kept version is no better than the one before it is at most %s%%. "+
			"The headline is tested separately on held-out tasks the loop never used.", formatP(100*alpha)))
	}
	if rec != nil && rec.Study == "" && !rec.InStudy {
		out = append(out, "One run of the loop. Run it twice more from version 0 to see whether the gain repeats.")
	}
	return out
}

func committedRounds(rec *Record) int {
	n := 0
	for _, rd := range rec.Rounds {
		if rd.Decision == DecisionCommitted {
			n++
		}
	}
	return n
}
