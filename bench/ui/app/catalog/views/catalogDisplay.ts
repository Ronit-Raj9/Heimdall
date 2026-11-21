import type { CatalogEntry } from "@/lib/types/catalog";

/** The human name and one-line description shown on a catalog card. */
export interface CatalogDisplay {
	title: string;
	description: string;
}

/**
 * Curated names and descriptions for every catalog entry id. The server's own
 * titles and summaries are technical (importer names, suite ids, formats);
 * those stay in the details drawer only.
 */
export const CATALOG_DISPLAY: Record<string, CatalogDisplay> = {
	// Headline benchmarks.
	"terminal-bench-2": { title: "Terminal-Bench 2.0", description: "Hard real-world tasks an agent completes in a Linux terminal" },
	skillsbench: { title: "SkillsBench", description: "Practical skills in a terminal (file handling, data, tooling)" },
	livecodebench: { title: "LiveCodeBench", description: "Coding problems with tests an honest agent cannot pass by cheating" },
	"swe-rebench": { title: "SWE-rebench", description: "Fix real GitHub issues in open-source Python projects" },
	"impossible-swebench": { title: "Impossible SWE-bench", description: "GitHub issues with broken tests, to catch agents that game the grader" },

	// Public benchmarks brought in from your own copy of the data.
	"import-agentdojo": { title: "AgentDojo", description: "Tool-using tasks laced with prompt injection attacks" },
	"import-browsecomp-plus": { title: "BrowseComp-Plus", description: "Hard questions answered by searching a fixed set of web pages" },
	"import-gaia": { title: "GAIA", description: "General assistant questions that need research and reasoning" },
	"import-harbor": { title: "Harbor tasks", description: "Any benchmark published in the Harbor task format" },
	"import-impossiblebench": { title: "ImpossibleBench", description: "Coding tasks with conflicting tests, to see if an agent cheats" },
	"import-kernelbench": { title: "KernelBench", description: "Write fast GPU kernels that match a reference implementation" },
	"import-locomo": { title: "LoCoMo", description: "Answer questions about very long conversations" },
	"import-longmemeval": { title: "LongMemEval", description: "Remember facts across long chat histories" },
	"import-marble": { title: "MARBLE", description: "Teams of agents that coordinate on a shared goal" },
	"import-mcpmark": { title: "MCPMark", description: "Real tasks done through MCP servers like GitHub and Notion" },
	"import-swebench": { title: "SWE-bench", description: "Fix real GitHub issues, checked by the project's own tests" },
	"import-tau2": { title: "tau2-bench", description: "Customer service with tools and a simulated customer" },

	// Example projects.
	"example-acme-a2a": { title: "A2A agents", description: "Agents served over the A2A protocol behind a gateway" },
	"example-acme-design": { title: "Design files", description: "Tasks graded on the files an agent produces" },
	"example-acme-desktop": { title: "Computer use", description: "An agent that works a Linux desktop from screenshots" },
	"example-acme-external": { title: "External API", description: "An agent that has to call a real HTTP service" },
	"example-acme-gpu": { title: "ML engineering", description: "Train models and speed up GPU code" },
	"example-acme-helpdesk": { title: "Helpdesk chat", description: "A support agent talking with a customer" },
	"example-acme-memory": { title: "Memory", description: "Does an agent remember what it learned in earlier sessions?" },
	"example-acme-node": { title: "Node.js project", description: "Coding tasks on a small Node.js codebase with a custom agent" },
	"example-acme-research": { title: "Web research", description: "Answer questions by searching the web and citing sources" },
	"example-acme-safety": { title: "Safety probes", description: "Does a support agent stay safe when its tools misbehave?" },
	"example-acme-stack": { title: "Multi-container app", description: "Tasks on an app running with Postgres and Redis" },
	"example-acme-support": { title: "Support desk", description: "A support agent working tickets through MCP tools" },
	"example-acme-team": { title: "Agent team", description: "A lead, two workers and a reviewer working together" },
	"example-acme-visual": { title: "Visual results", description: "Tasks graded on how the result looks" },
	"example-acme-voice-desk": { title: "Voice support", description: "A voice agent handling support calls" },
	"example-acme-web": { title: "Browser back office", description: "An agent that runs an admin console in the browser" },

	// Suites bundled with the server.
	"suite-coding-core": { title: "Coding core", description: "Small Python repositories with hidden tests" },
	"suite-hd-harbor-tb2": { title: "Terminal-Bench 2.0", description: "Hard real-world tasks an agent completes in a Linux terminal" },
	"suite-hd-harbor-skills": { title: "SkillsBench", description: "Practical skills in a terminal (file handling, data, tooling)" },
	"suite-hd-swe-ib-lcb": { title: "LiveCodeBench", description: "Coding problems with tests an honest agent cannot pass by cheating" },
	"suite-hd-swe-rebench": { title: "SWE-rebench", description: "Fix real GitHub issues in open-source Python projects" },
	"suite-hd-swe-ib-swe": { title: "Impossible SWE-bench", description: "GitHub issues with broken tests, to catch agents that game the grader" },
};

function cap(s: string): string {
	return s ? s[0].toUpperCase() + s.slice(1) : s;
}

/** First sentence of a summary, cut to about 90 characters. */
function oneLine(s: string | undefined): string {
	const first = (s ?? "").split(/(?<=\.)\s/)[0].replace(/\.$/, "").trim();
	return first.length > 90 ? `${first.slice(0, 87).trimEnd()}...` : first;
}

/** Fallback name for ids not in the map: the title without "Acme ... example" noise. */
function fallbackTitle(e: CatalogEntry): string {
	const m = /^acme\s+(.*?)\s*example/i.exec(e.title.trim());
	if (m?.[1]) return cap(m[1].trim());
	if (/^[a-z0-9_-]+$/.test(e.title)) return cap(e.title.replace(/^(hd|suite)-/, "").replace(/[-_]/g, " "));
	return e.title;
}

/** Fallback description: the tagline after a colon in the title, else the summary's first sentence. */
function fallbackDescription(e: CatalogEntry): string {
	const colon = e.title.indexOf(":");
	if (colon > 0) return cap(e.title.slice(colon + 1).trim());
	return oneLine(e.summary);
}

/** The card name and line for an entry: curated when known, else cleaned from the server's text. */
export function displayFor(e: CatalogEntry): CatalogDisplay & { example: boolean } {
	const curated = CATALOG_DISPLAY[e.id];
	return {
		title: curated?.title ?? fallbackTitle(e),
		description: curated?.description ?? fallbackDescription(e),
		example: e.kind === "example",
	};
}
