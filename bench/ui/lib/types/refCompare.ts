// Gap lane G07: params and results of experiment.compare-refs and blame.replay
// (cmd/bifrost-bench/jobs_comparerefs.go, jobs_blamereplay.go).

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		refCompare: typeof import("@/locales/en/refCompare.json");
	}
}

export interface CompareRefsJobParams {
	agent: string;
	ref_a: string;
	ref_b: string;
	suite: string;
	/** owner/name on GitHub; absent: the project's own git repository (local server only). */
	repo?: string;
	tasks?: string[];
	trials?: number;
	budget_usd?: number;
}

export interface BlameReplayJobParams {
	exp_id: string;
	run_id?: string;
	stub?: string;
	max?: number;
}

export interface BlameReplayItem {
	run_id: string;
	replay_exp_id?: string;
	status: string;
	tier: string;
	agent?: string;
	detail?: string;
}

export interface BlameReplayResult {
	exp_id: string;
	candidates: number;
	replays: BlameReplayItem[];
}

/** The compare-refs params, or null while a required field is missing or the refs match. */
export function compareRefsParams(f: {
	agent: string;
	suite: string;
	repo: string;
	refA: string;
	refB: string;
	tasks: string;
	trials: number;
	budget: number;
}): CompareRefsJobParams | null {
	const refA = f.refA.trim();
	const refB = f.refB.trim();
	if (!f.agent || !f.suite.trim() || !refA || !refB || refA === refB) return null;
	const p: CompareRefsJobParams = {
		agent: f.agent,
		suite: f.suite.trim(),
		ref_a: refA,
		ref_b: refB,
		trials: Math.max(1, Math.floor(f.trials) || 1),
	};
	if (f.repo) p.repo = f.repo;
	const tasks = [
		...new Set(
			f.tasks
				.split(",")
				.map((s) => s.trim())
				.filter(Boolean),
		),
	];
	if (tasks.length) p.tasks = tasks;
	if (f.budget > 0) p.budget_usd = f.budget;
	return p;
}
