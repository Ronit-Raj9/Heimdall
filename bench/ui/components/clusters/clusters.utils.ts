import type { ClusterExclusion, ClusterLayer, ClusterParams, ClusterScope, ClusterSynthetic } from "@/lib/types/bench";

// Pure helpers for the failure clusters screen. A cluster is an observed grouping of failed runs,
// never a root cause (docs/failure-clusters.md).

/** The screen's URL query: /clusters?suite=&agent=&exp=&since=&min=&synthetic=&sig=. Null means absent. */
export interface ClusterQuery {
	suite: string | null;
	agent: string | null;
	/** Experiment ids, comma separated, as typed. */
	exp: string | null;
	since: string | null;
	min: number | null;
	synthetic: ClusterSynthetic | null;
}

/** Experiment ids from a comma list: trimmed, empty ones dropped, first occurrence kept. */
export function parseExpList(value: string | null | undefined): string[] {
	if (!value) return [];
	const out: string[] = [];
	for (const part of value.split(",")) {
		const id = part.trim();
		if (id && !out.includes(id)) out.push(id);
	}
	return out;
}

/** A comma list for the URL, or null when there is nothing to keep (the param is then dropped). */
export function formatExpList(ids: readonly string[]): string | null {
	return ids.length > 0 ? ids.join(",") : null;
}

/** The request params for a query. Blank text and a minimum below 1 are left out so the server default applies. */
export function clusterParams(query: ClusterQuery): ClusterParams {
	const params: ClusterParams = {};
	const suite = query.suite?.trim();
	const agent = query.agent?.trim();
	const since = query.since?.trim();
	const exp = formatExpList(parseExpList(query.exp));
	if (suite) params.suite = suite;
	if (agent) params.agent = agent;
	if (exp) params.exp = exp;
	if (since) params.since = since;
	if (query.min !== null && Number.isInteger(query.min) && query.min >= 1) params.min = query.min;
	if (query.synthetic) params.synthetic = query.synthetic;
	return params;
}

/** True when any filter is set in the URL. */
export function hasClusterFilters(query: ClusterQuery): boolean {
	return Object.keys(clusterParams(query)).length > 0;
}

/** A 0..1 share as a percent for the UI locale, at most one decimal. Out of range values are clamped. */
export function formatShare(share: number, locale: string): string {
	const value = Number.isFinite(share) ? Math.min(1, Math.max(0, share)) : 0;
	return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 1 }).format(value);
}

/** The first `max` items and how many more there are, for a compact table cell. */
export function shownItems(items: readonly string[] | null | undefined, max = 2): { shown: string[]; more: number } {
	const list = items ?? [];
	return { shown: list.slice(0, max), more: Math.max(0, list.length - max) };
}

/** Every scope, narrowest first. */
export const CLUSTER_SCOPES = ["single_run", "agent_and_task", "agent", "task", "systemic"] as const satisfies readonly ClusterScope[];

/** True for a scope the screen has a label for. */
export function isClusterScope(scope: string): scope is ClusterScope {
	return (CLUSTER_SCOPES as readonly string[]).includes(scope);
}

/** Every layer kind the screen has a label for, most specific first. */
export const CLUSTER_LAYER_KINDS = [
	"outcome",
	"failed_tests",
	"failed_dimensions",
	"answer_category",
	"state_checks",
	"gates",
	"loop",
	"error",
	"provider",
	"last_tool",
	"condition",
] as const satisfies readonly ClusterLayer["kind"][];

/** True for a layer kind the screen has a label for. */
export function isClusterLayerKind(kind: string): kind is ClusterLayer["kind"] {
	return (CLUSTER_LAYER_KINDS as readonly string[]).includes(kind);
}

/**
 * True for a layer whose items are failed hidden test ids. The screen lists those the way the run
 * page lists grade.json's failed tests, and nowhere else.
 */
export function isHiddenTestLayer(layer: ClusterLayer): boolean {
	return layer.kind === "failed_tests" && (layer.items ?? []).length > 0;
}

/** True when the server left synthetic runs out, so the empty state can offer them. */
export function excludesSynthetic(excluded: readonly ClusterExclusion[] | null | undefined): boolean {
	return (excluded ?? []).some((group) => group.reason === "synthetic" && group.runs > 0);
}

/** True for a request that failed with 404: the signature is not in the filtered set. */
export function isNotFound(error: unknown): boolean {
	return Boolean(error && typeof error === "object" && "status" in error && (error as { status: unknown }).status === 404);
}