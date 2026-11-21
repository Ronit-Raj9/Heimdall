// The experiment page's tabs and when each applies. Pure, so the rule is tested without a page.

export const EXPERIMENT_TABS = [
	"verdict",
	"runs",
	"matrix",
	"cost",
	"migration",
	"adaptive",
	"scorecards",
	"receipts",
	"integrity",
	"export",
	"actions", // web parity L14: follow-up actions (actionsTab.tsx)
] as const;

export type ExperimentTabId = (typeof EXPERIMENT_TABS)[number];

/** What the page knows about an experiment that decides its tabs. */
export interface ExperimentFacts {
	/** Arm ids from the manifest (compatibility arm excluded). */
	arms: number;
	/** The experiment has a trial plan or a suite tier (GET /experiments/{id}/adaptive). */
	adaptive: boolean;
	/** A support, research or routing scorecard exists for it. */
	scorecards: boolean;
	/** At least one probe condition beside clean, so there are fault receipts to read. */
	probes: boolean;
}

/**
 * Tabs that do not apply are hidden, never shown empty: the matrix needs three arms (two arms are
 * the verdict itself), migration two, adaptive a trial plan or tier, scorecards a support, research
 * or routing summary, receipts a probe condition. Verdict, runs, cost, integrity and export always apply.
 */
export function hiddenExperimentTabs(facts: ExperimentFacts): ReadonlySet<ExperimentTabId> {
	const hidden = new Set<ExperimentTabId>();
	if (facts.arms < 3) hidden.add("matrix");
	if (facts.arms < 2) hidden.add("migration");
	if (!facts.adaptive) hidden.add("adaptive");
	if (!facts.scorecards) hidden.add("scorecards");
	if (!facts.probes) hidden.add("receipts");
	return hidden;
}
/**
 * The tab an experiment opens on without ?tab=: the matrix for three or more arms, whose ranked
 * comparison covers every arm (the verdict reads only the primary pair), else the verdict
 * (QC2-29-02).
 */
export function defaultExperimentTab(arms: number): ExperimentTabId {
	return arms >= 3 ? "matrix" : "verdict";
}
