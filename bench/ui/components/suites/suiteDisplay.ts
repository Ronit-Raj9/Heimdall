import type { SuiteSummary } from "@/lib/types/bench";

// Lane R02: how a benchmark reads to a person. Suites carry only an id and a technical description,
// so known ids get a curated name and one-line description; unknown ids fall back to a cleaned-up
// version of what the pack says about itself.

export interface SuiteDisplay {
	name: string;
	description: string;
	category?: string;
}

const SUITE_DISPLAY: Record<string, SuiteDisplay> = {
	"coding-core": { name: "Coding basics", description: "Fix small Python projects so their hidden tests pass." },
	// Built-in library (suite/library.go): full real benchmarks every org can run.
	"hd-harbor-tb2": { name: "Terminal-Bench 2", description: "Hard command-line tasks in a real Linux shell." },
	"hd-harbor-skills": { name: "SkillsBench", description: "Tasks that need the agent to find and use the right skill." },
	"hd-swe-verified": { name: "SWE-bench Verified", description: "Fix real GitHub issues in popular Python projects." },
	"hd-swe-rebench": { name: "SWE-rebench", description: "Fresh real GitHub issues the models have not seen in training." },
	"hd-swe-ib-swe": {
		name: "ImpossibleBench SWE",
		description: "GitHub issues whose tests are rigged; passing means the agent cheated.",
	},
	"hd-swe-ib-lcb": {
		name: "ImpossibleBench LiveCodeBench",
		description: "Coding puzzles whose tests are rigged; passing means the agent cheated.",
	},
	longmemeval: { name: "LongMemEval", description: "Answer questions about long chat histories from many sessions." },
	"longmemeval-oracle": {
		name: "LongMemEval Oracle",
		category: "memory",
		description: "Answer questions about long chat histories from many sessions.",
	},
	locomo: { name: "LoCoMo", description: "Remember facts across very long conversations." },
	tau2: { name: "tau2-bench", description: "Help customers over chat while following company policy and tools." },
	agentdojo: { name: "AgentDojo", description: "Do real tool tasks while resisting prompt injection attacks." },
	"agentdojo-banking": {
		name: "AgentDojo: Banking",
		category: "safety",
		description: "Do real tasks with tools while ignoring hidden attack instructions.",
	},
	"agentdojo-slack": {
		name: "AgentDojo: Slack",
		category: "safety",
		description: "Do real tasks with tools while ignoring hidden attack instructions.",
	},
	"agentdojo-travel": {
		name: "AgentDojo: Travel",
		category: "safety",
		description: "Do real tasks with tools while ignoring hidden attack instructions.",
	},
	"agentdojo-workspace": {
		name: "AgentDojo: Workspace",
		category: "safety",
		description: "Do real tasks with tools while ignoring hidden attack instructions.",
	},
	mcpmark: { name: "MCPMark", description: "Get real jobs done through MCP tool servers." },
	marble: { name: "MARBLE: Database", category: "team", description: "Several agents work together on one shared task." },
	"marble-coding": { name: "MARBLE: Coding", category: "team", description: "Several agents work together on one shared task." },
	"marble-research": { name: "MARBLE: Research", category: "team", description: "Several agents work together on one shared task." },
	"browsecomp-plus": { name: "BrowseComp-Plus", description: "Hard research questions answered by searching a fixed web corpus." },
	"browsecomp-plus-0": {
		name: "BrowseComp-Plus · Part 1",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-1": {
		name: "BrowseComp-Plus · Part 2",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-2": {
		name: "BrowseComp-Plus · Part 3",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-3": {
		name: "BrowseComp-Plus · Part 4",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-4": {
		name: "BrowseComp-Plus · Part 5",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-5": {
		name: "BrowseComp-Plus · Part 6",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-judged-0": {
		name: "BrowseComp-Plus Open · Part 1",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-judged-1": {
		name: "BrowseComp-Plus Open · Part 2",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-judged-2": {
		name: "BrowseComp-Plus Open · Part 3",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-judged-3": {
		name: "BrowseComp-Plus Open · Part 4",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-judged-4": {
		name: "BrowseComp-Plus Open · Part 5",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	"browsecomp-plus-judged-5": {
		name: "BrowseComp-Plus Open · Part 6",
		category: "research",
		description: "Find hard-to-locate facts by searching a fixed web collection.",
	},
	kernelbench: { name: "KernelBench", description: "Write fast GPU kernels; needs a GPU worker." },
	"kernelbench-l2": { name: "KernelBench Level 2", category: "coding", description: "Write fast GPU kernels; needs a GPU worker." },
	"kernelbench-l3": { name: "KernelBench Level 3", category: "coding", description: "Write fast GPU kernels; needs a GPU worker." },
	voicebench: { name: "VoiceBench", description: "Spoken questions answered by a voice agent." },
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

/** The curated capability group ("memory", "safety", ...) when the id has one; undefined otherwise. */
export function suiteDisplayCategory(id: string): string | undefined {
	return SUITE_DISPLAY[id]?.category;
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

// Built-in library order (most wanted first); library ids not listed sort after, by name.
const LIBRARY_ORDER = [
	"hd-harbor-tb2",
	"hd-swe-verified",
	"hd-swe-rebench",
	"hd-harbor-skills",
	"hd-swe-ib-swe",
	"hd-swe-ib-lcb",
	"tau2",
	"agentdojo",
	"agentdojo-banking",
	"agentdojo-slack",
	"agentdojo-travel",
	"agentdojo-workspace",
	"mcpmark",
	"browsecomp-plus",
	"browsecomp-plus-0",
	"browsecomp-plus-1",
	"browsecomp-plus-2",
	"browsecomp-plus-3",
	"browsecomp-plus-4",
	"browsecomp-plus-5",
	"browsecomp-plus-judged-0",
	"browsecomp-plus-judged-1",
	"browsecomp-plus-judged-2",
	"browsecomp-plus-judged-3",
	"browsecomp-plus-judged-4",
	"browsecomp-plus-judged-5",
	"longmemeval",
	"longmemeval-oracle",
	"locomo",
	"marble",
	"marble-coding",
	"marble-research",
	"voicebench",
	"kernelbench",
	"kernelbench-l2",
	"kernelbench-l3",
];

/** Every id the built-in library ships (catalog "Built in" badges use the server flag first). */
export const LIBRARY_SUITE_IDS: readonly string[] = LIBRARY_ORDER;

/** The small made-up practice suites: the Acme examples and the coding-core starter. */
export function isPracticeSuite(id: string): boolean {
	return id.startsWith("acme-") || id === "coding-core";
}

/** A pack served from the server's read-only built-in library. */
export function isBuiltinSuite(suite: Pick<SuiteSummary, "builtin" | "source">): boolean {
	return suite.builtin === true || suite.source === "library";
}

function libraryRank(id: string): number {
	const i = LIBRARY_ORDER.indexOf(id);
	return i < 0 ? LIBRARY_ORDER.length : i;
}

export interface SuiteSections<T> {
	/** Built-in library benchmarks: full real suites, first. */
	builtin: T[];
	/** What the organization added itself (catalog imports, uploads, its own). */
	org: T[];
	/** Acme and coding-core: small, made-up data, shown collapsed. */
	practice: T[];
}

type SectionFields = Pick<SuiteSummary, "id" | "builtin" | "source">;

/** Splits suites into built-in, org-added and practice examples, each in display order. */
export function suiteSections<T extends SectionFields>(suites: readonly T[]): SuiteSections<T> {
	const out: SuiteSections<T> = { builtin: [], org: [], practice: [] };
	for (const s of suites) {
		if (isBuiltinSuite(s)) out.builtin.push(s);
		else if (isPracticeSuite(s.id)) out.practice.push(s);
		else out.org.push(s);
	}
	const byName = (a: T, b: T) => suiteDisplayName(a.id).localeCompare(suiteDisplayName(b.id)) || a.id.localeCompare(b.id);
	out.builtin.sort((a, b) => libraryRank(a.id) - libraryRank(b.id) || byName(a, b));
	out.org.sort(byName);
	out.practice.sort((a, b) => Number(b.id === "coding-core") - Number(a.id === "coding-core") || byName(a, b));
	return out;
}

/** Suites in display order: built-in first, then org-added, then practice examples. */
export function orderSuites<T extends SectionFields>(suites: readonly T[]): T[] {
	const s = suiteSections(suites);
	return [...s.builtin, ...s.org, ...s.practice];
}