// Web parity lane L14: params and results of the experiment.extend, experiment.migrate and
// probe.run job kinds (cmd/bifrost-bench/jobs_experiment.go).

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		experimentActions: typeof import("@/locales/en/experimentActions.json");
	}
}

export interface ExtendJobParams {
	exp_id: string;
	tasks?: number;
	trials?: number;
	force?: boolean;
}

export interface ProbeJobParams {
	exp_id: string;
	/** The benchmark's own probes; empty or absent with no faults: every probe of the suite. */
	probes?: string[];
	/** Fault catalog names (GET /faults), such as model-brownout; mapped to conditions on the benchmark (fix wave 2, N65). */
	faults?: string[];
	trials?: number;
	/** The fault test's spend cap in USD, required by the server (K5). */
	budget_usd?: number;
}

export interface MigrateJobParams {
	agent: string;
	from: string;
	to: string[];
	suite: string;
	trials?: number;
	budget_usd?: number;
}

/** The result each of the three kinds returns. */
export interface FollowUpResult {
	exp_id: string;
	parent?: string;
	file: string;
	slots: number;
	started: boolean;
	notes?: string[];
}

export const EXTEND_MAX_TRIALS = 50;
export const EXTEND_MAX_TASKS = 500;

/** Splits a comma-separated model list, trimming blanks and duplicates. */
export function splitModels(text: string): string[] {
	const out: string[] = [];
	for (const part of text.split(",")) {
		const m = part.trim();
		if (m && !out.includes(m)) out.push(m);
	}
	return out;
}

/** The migrate params, or null while a required field is missing. */
export function migrateParams(f: {
	agent: string;
	suite: string;
	from: string;
	to: string;
	trials: number;
	budget: number;
}): MigrateJobParams | null {
	const to = splitModels(f.to).filter((m) => m !== f.from.trim());
	if (!f.agent || !f.suite.trim() || !f.from.trim() || to.length === 0) return null;
	const p: MigrateJobParams = {
		agent: f.agent,
		suite: f.suite.trim(),
		from: f.from.trim(),
		to,
		trials: Math.max(1, Math.floor(f.trials) || 1),
	};
	if (f.budget > 0) p.budget_usd = f.budget;
	return p;
}

/** The follow-up result of a finished job, when it has one. */
export function followUpOf(result: unknown): FollowUpResult | null {
	if (!result || typeof result !== "object") return null;
	const r = result as Partial<FollowUpResult>;
	return typeof r.exp_id === "string" && r.exp_id ? (r as FollowUpResult) : null;
}