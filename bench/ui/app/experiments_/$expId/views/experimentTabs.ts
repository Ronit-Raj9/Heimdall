// The experiment page's tabs and when each applies. Pure, so the rule is tested without a page.

// M4-15: Result, Runs, Cost and Checks first; the rest after them. Extra trials (adaptive) merged into
// Actions and Export into the header menu: both stay in the list so an old ?tab= link resolves (redirectExperimentTab).
export const EXPERIMENT_TABS = [
	"verdict",
	"runs",
	"cost",
	"integrity",
	"matrix",
	"migration",
	"actions", // web parity L14: follow-up actions (actionsTab.tsx)
	"scorecards",
	"receipts",
	"adaptive",
	"export",
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
	// Never shown in the strip (M4-15): their content moved to Actions and to the header menu.
	const hidden = new Set<ExperimentTabId>(["adaptive", "export"]);
	if (facts.arms < 3) hidden.add("matrix");
	if (facts.arms < 2) hidden.add("migration");
	if (!facts.adaptive) hidden.add("adaptive");
	if (!facts.scorecards) hidden.add("scorecards");
	if (!facts.probes) hidden.add("receipts");
	return hidden;
}
/**
 * The tab an experiment opens on without ?tab=: always the Result (M4-51). Its summary names the
 * leader or the tie of every setup, so a three-arm matrix no longer needs to be the first screen
 * (it was, QC2-29-02).
 */
export function defaultExperimentTab(_arms: number): ExperimentTabId {
	return "verdict";
}

/** Where an old ?tab= value now lives (M4-15): Extra trials in Actions, Export in the header menu (Result); null when it stays. */
export function redirectExperimentTab(tab: string | null | undefined): ExperimentTabId | null {
	if (tab === "adaptive") return "actions";
	if (tab === "export") return "verdict";
	return null;
}