import type { ReleaseRecord } from "@/lib/types/bench";

/** The agent a release put live and its version (0: the record names no version). */
export interface ReleaseAgent {
	agent: string;
	version: number;
}

/**
 * Which agent version a release record put live (Q-16). A release from an evaluation names
 * agent_id and version; a release recorded from a comparison carries only agent_config_id, which
 * is the agent ref ("codex", "agents/codex.yaml" or "codex@v2"), so the agent and any pinned
 * version come from it the way the server reads a ref (reportcard.AgentRef). Null when neither
 * names an agent.
 */
export function releaseAgent(r: Pick<ReleaseRecord, "agent_id" | "version" | "agent_config_id">): ReleaseAgent | null {
	if (r.agent_id) return { agent: r.agent_id, version: r.version ?? 0 };
	let ref = (r.agent_config_id ?? "").trim();
	if (!ref) return null;
	let version = 0;
	const m = /@v([0-9]+)$/.exec(ref);
	if (m) {
		version = Number(m[1]);
		ref = ref.slice(0, -m[0].length);
	}
	ref = ref
		.replace(/^agents\//, "")
		.replace(/\.yaml$/, "")
		.replace(/\.yml$/, "");
	return ref ? { agent: ref, version } : null;
}

/**
 * What is live now, the one rule every page that says "Live now" reads on the client: the newest
 * release of each agent, newest agent first, from both kinds of record. Returns the record with
 * its resolved agent.
 */
export function liveReleases(releases: readonly ReleaseRecord[]): { release: ReleaseRecord; agent: ReleaseAgent }[] {
	const seen = new Set<string>();
	const out: { release: ReleaseRecord; agent: ReleaseAgent }[] = [];
	const sorted = [...releases].sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""));
	for (const r of sorted) {
		const a = releaseAgent(r);
		if (!a || seen.has(a.agent)) continue;
		seen.add(a.agent);
		out.push({ release: r, agent: a });
	}
	return out;
}
