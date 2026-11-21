import type { SuiteSummary } from "@/lib/types/bench";

// Lane R02: how a benchmark reads to a person. Suites carry only an id and a technical description,
// so known ids get a curated name and one-line description; unknown ids fall back to a cleaned-up
// version of what the pack says about itself.

export interface SuiteDisplay {
	name: string;
	description: string;
}

const SUITE_DISPLAY: Record<string, SuiteDisplay> = {
	"coding-core": { name: "Coding basics", description: "Fix small Python projects so their hidden tests pass." },
	"hd-harbor-tb2": { name: "Terminal-Bench 2", description: "Hard command-line tasks in a real Linux shell." },
	"hd-swe-ib-lcb": {
		name: "LiveCodeBench (honesty)",
		description: "Coding puzzles whose tests are rigged; passing means the agent cheated.",
	},
	"acme-orders": { name: "Node.js bug fixing", description: "Find and fix bugs in a small Node.js order service." },
	"acme-stack": { name: "App with database", description: "Change a web app and its database without breaking either." },
	"acme-memory": { name: "Memory across sessions", description: "Remember what a user said earlier and use it later." },
	"acme-desktop": { name: "Computer use (desktop)", description: "Get everyday jobs done by clicking and typing on a desktop." },
	"acme-web": { name: "Browser tasks", description: "Find things and fill in forms on real-looking websites." },
	"acme-helpdesk": { name: "Customer support chat", description: "Answer customer questions politely and correctly." },
	"acme-support": { name: "Support desk with tools", description: "Resolve support tickets by looking things up and taking action." },
	"acme-voice-desk": { name: "Voice support calls", description: "Handle a customer's problem over a spoken phone call." },
	"acme-research": { name: "Web research", description: "Search the web and come back with a correct, sourced answer." },
	"acme-safety": { name: "Safety under attack", description: "Do the job while ignoring tricks that try to cause harm." },
	"acme-team": { name: "Team of agents", description: "Several agents split up a job and finish it together." },
};

const MAX_DESCRIPTION = 90;

function titleCase(id: string): string {
	const words = id
		.replace(/[-_]+/g, " ")
		.trim()
		.split(/\s+/)
		.filter(Boolean)
		.map((w) => w.charAt(0).toUpperCase() + w.slice(1));
	return words.length ? words.join(" ") : id;
}

/** "acme-orders" reads as "Node.js bug fixing"; an unknown "my-pack" as "My Pack". */
export function suiteDisplayName(id: string): string {
	return SUITE_DISPLAY[id]?.name ?? titleCase(id);
}

/**
 * The first sentence of a pack's own description with the jargon taken out: parenthetical text,
 * file names, tool-ish tokens (name@version, snake_case, paths). Cut at about 90 characters.
 */
export function plainDescription(raw: string | undefined | null): string {
	if (!raw) return "";
	let text = raw.replace(/\s+/g, " ").trim();
	const sentence = text.match(/^.*?[.!?](?=\s|$)/);
	if (sentence) text = sentence[0];
	text = text
		.replace(/\([^)]*\)/g, "")
		.replace(/\[[^\]]*\]/g, "")
		.replace(/`[^`]*`/g, "")
		.replace(/\S*@\S+/g, "")
		.replace(/\S*\/\S*/g, "")
		.replace(/\b\S+\.(?:py|ts|tsx|js|json|toml|ya?ml|md|sh|txt)\b/gi, "")
		.replace(/\b\w+_\w+\b/g, "")
		.replace(/\s+([,.;:!?])/g, "$1")
		.replace(/,(\s*,)+/g, ",")
		.replace(/\s{2,}/g, " ")
		.replace(/^[\s,;:]+|[\s,;:]+$/g, "")
		.trim();
	if (text.length > MAX_DESCRIPTION) {
		const cut = text.slice(0, MAX_DESCRIPTION);
		const space = cut.lastIndexOf(" ");
		text = `${(space > 40 ? cut.slice(0, space) : cut).replace(/[\s,;:.]+$/, "")}...`;
	}
	return text ? text.charAt(0).toUpperCase() + text.slice(1) : "";
}

/** One short human sentence for the benchmark: curated for known ids, cleaned up otherwise. */
export function suiteDisplayDescription(suite: Pick<SuiteSummary, "id" | "description">): string {
	return SUITE_DISPLAY[suite.id]?.description ?? plainDescription(suite.description);
}

/** "Coding basics" plus its tooltip id, for places that want both. */
export function suiteDisplay(suite: Pick<SuiteSummary, "id" | "description">): SuiteDisplay {
	return { name: suiteDisplayName(suite.id), description: suiteDisplayDescription(suite) };
}
