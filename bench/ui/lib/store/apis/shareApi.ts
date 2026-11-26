import { withOrgParam } from "./orgParam";
import { BENCH_API_BASE } from "./fixtures";
import { benchApi } from "./benchApi";

/**
 * Share links (lane X14, bench/handlers/sharelinks.go): read-only signed links to one report card,
 * evaluation, verdict, run trace or leaderboard view, plus report card exports and the reproduce
 * bundle. Types mirror the Go JSON.
 */

export type ShareKind = "evaluation" | "report_card" | "verdict" | "run_trace" | "leaderboard";
export type ShareState = "active" | "expired" | "revoked";

export interface Share {
	id: string;
	kind: ShareKind;
	target: string;
	query?: string;
	label?: string;
	hide_prompts: boolean;
	created_at: string;
	created_by?: string;
	expires_at?: string;
	revoked_at?: string;
	revoked_by?: string;
	state: ShareState;
	/** Only on active links. */
	token?: string;
	path?: string;
	url?: string;
}

export interface NewShare {
	kind: ShareKind;
	target: string;
	query?: string;
	label?: string;
	hide_prompts?: boolean;
	/** 0 means never; omitted means 30. */
	expires_in_days?: number;
}

export interface SharedMeta {
	kind: ShareKind;
	target: string;
	query?: string;
	label?: string;
	hide_prompts: boolean;
	org?: string;
	created_at: string;
	expires_at?: string;
	exports?: string[];
	badge: boolean;
}

/** GET /shared/{token}: the item's parts, keyed by name (evaluation, report, report_card, ...). */
export interface SharedItem {
	share: SharedMeta;
	data: Record<string, unknown>;
	/** The link's opaque token (s_...) when it was opened with an old token that names the organization. */
	canonical_token?: string;
}

const enc = encodeURIComponent;

/** Absolute URL of an active link, on this server's origin unless the server named its public URL. */
export function shareUrl(
	share: Pick<Share, "url" | "path">,
	origin: string = typeof window === "undefined" ? "" : window.location.origin,
): string {
	if (share.url) return share.url;
	return share.path ? `${origin}${share.path}` : "";
}

/** The badge image of a link, and the Markdown that embeds it with a link to the shared page. */
export function badgeUrl(token: string, origin: string = typeof window === "undefined" ? "" : window.location.origin): string {
	return `${origin}${BENCH_API_BASE}/shared/${enc(token)}/badge.svg`;
}

export function badgeMarkdown(token: string, pageUrl: string, label = "Heimdall report card", origin?: string): string {
	return `[![${label}](${badgeUrl(token, origin)})](${pageUrl})`;
}

/** Download URLs of the report card exports and the reproduce bundle (org members). */
export function reportExportUrl(evaluationId: string, format: "html" | "md" | "json"): string {
	return withOrgParam(`${BENCH_API_BASE}/evaluations/${enc(evaluationId)}/report/export?format=${format}`);
}

export function reproduceUrl(evaluationId: string): string {
	return withOrgParam(`${BENCH_API_BASE}/evaluations/${enc(evaluationId)}/reproduce`);
}

/** Export URL of a shared evaluation (anyone holding the link). */
export function sharedExportUrl(token: string, format: "html" | "md"): string {
	return `${BENCH_API_BASE}/shared/${enc(token)}/export?format=${format}`;
}

export const shareApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchShares"] }).injectEndpoints({
	endpoints: (builder) => ({
		listShares: builder.query<{ shares: Share[] }, { kind?: ShareKind; target?: string } | void>({
			query: (p) => ({
				url: "/shares",
				params: {
					...(p?.kind ? { kind: p.kind } : {}),
					...(p?.target ? { target: p.target } : {}),
				},
			}),
			providesTags: ["BenchShares"],
		}),
		createShare: builder.mutation<Share, NewShare>({
			query: (body) => ({ url: "/shares", method: "POST", body }),
			invalidatesTags: ["BenchShares"],
		}),
		revokeShare: builder.mutation<Share, string>({
			query: (id) => ({ url: `/shares/${enc(id)}/revoke`, method: "POST" }),
			invalidatesTags: ["BenchShares"],
		}),
		getShared: builder.query<SharedItem, string>({
			query: (token) => `/shared/${enc(token)}`,
		}),
	}),
});

export const { useListSharesQuery, useCreateShareMutation, useRevokeShareMutation, useGetSharedQuery } = shareApi;