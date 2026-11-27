// Pure helpers for what the Patterns views open on (M7-41): a comparison with finished runs, and
// the agent that ran last. No I/O, so each is tested alone.

/** The first listed comparison that has finished runs, else the first listed one. */
export function firstWithFinishedRuns(
	options: readonly { id: string }[],
	experiments: readonly { exp_id: string; slots?: { done?: number } }[],
): string | undefined {
	const done = new Set(experiments.filter((e) => (e.slots?.done ?? 0) > 0).map((e) => e.exp_id));
	return options.find((o) => done.has(o.id))?.id ?? options[0]?.id;
}

/** The agent with the newest run, so "By agent version" opens on data; undefined when none ran. */
export function latestRunAgent(agents: readonly { id: string; last_run_at?: string | null }[]): string | undefined {
	let best: { id: string; at: number } | undefined;
	for (const a of agents) {
		const at = Date.parse(a.last_run_at ?? "");
		if (Number.isNaN(at)) continue;
		if (!best || at > best.at) best = { id: a.id, at };
	}
	return best?.id;
}