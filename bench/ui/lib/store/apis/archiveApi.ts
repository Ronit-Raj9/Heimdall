// Archive instead of delete (phase D, decision 6): evaluations, comparisons, runs, improvement runs
// and findings get POST /{kind}/{id}/archive and /unarchive. Archive only hides an object from its
// list; nothing is deleted and no score changes. Monitor tests (safety rule tests) get a real delete.
// Injected into benchApi so writes share its base query (CSRF header, org header, 401 handling).
import { benchApi } from "./benchApi";
import type { FindingState, FindingsListResponse } from "./findingsApi";

export type ArchiveKind = "evaluation" | "experiment" | "run" | "improvement" | "finding";

/** The list parameter: "include" shows archived rows beside the others, "only" shows just them. */
export type ArchivedParam = "include" | "only";

/** The least role the server lets archive and unarchive each kind (D1: admin, findings member). */
export const ARCHIVE_ROLE: Record<ArchiveKind, "admin" | "member"> = {
	evaluation: "admin",
	experiment: "admin",
	run: "admin",
	improvement: "admin",
	finding: "member",
};
/** The least role the server lets delete a monitor test. */
export const MONITOR_DELETE_ROLE = "admin" as const;

const COLLECTION: Record<ArchiveKind, string> = {
	evaluation: "evaluations",
	experiment: "experiments",
	run: "runs",
	improvement: "improvements",
	finding: "findings",
};

const TAG = {
	evaluation: "Evaluations",
	experiment: "BenchExperiment",
	run: "BenchRun",
	improvement: "BenchImprovement",
	finding: "BenchFindings",
} as const;

/** The route of an archive or unarchive call. Pure. */
export function archivePath(kind: ArchiveKind, id: string, undo = false): string {
	return `/${COLLECTION[kind]}/${encodeURIComponent(id)}/${undo ? "unarchive" : "archive"}`;
}

/** Tags a call refreshes: the kind's list and the object; an evaluation also refreshes comparisons. Pure. */
export function archiveTags(kind: ArchiveKind, id: string) {
	const tags: { type: (typeof TAG)[ArchiveKind]; id: string }[] = [
		{ type: TAG[kind], id: "LIST" },
		{ type: TAG[kind], id },
	];
	if (kind === "evaluation") tags.push({ type: "BenchExperiment", id: "LIST" });
	if (kind === "experiment") tags.push({ type: "BenchRun", id: "LIST" });
	return tags;
}

/** Whether a row is archived. The field is optional: a server without archive never sends it. Pure. */
export function isArchived(row: { archived_at?: string | null } | null | undefined): boolean {
	return typeof row?.archived_at === "string" && row.archived_at !== "";
}

/** The list parameter for the "Show archived" switch: left out when off. Pure. */
export function archivedParam(show: boolean): { archived?: ArchivedParam } {
	return show ? { archived: "include" } : {};
}

/**
 * The archived count a list answer carries, when the server gives one (archived_count, or
 * archived as a number); undefined otherwise. Pure.
 */
export function archivedCountOf(body: unknown): number | undefined {
	if (!body || typeof body !== "object") return undefined;
	const b = body as { archived_count?: unknown; archived?: unknown };
	if (typeof b.archived_count === "number" && b.archived_count >= 0) return b.archived_count;
	if (typeof b.archived === "number" && b.archived >= 0) return b.archived;
	return undefined;
}

/** What an archive call answers. Every field is optional: never assume the object left a list. */
export interface ArchiveResponse {
	kind?: ArchiveKind;
	id?: string;
	archived?: boolean;
	archived_at?: string | null;
	archived_by?: string;
}

export interface ArchiveArgs {
	kind: ArchiveKind;
	id: string;
}

export const archiveApi = benchApi
	.enhanceEndpoints({ addTagTypes: ["Evaluations", "BenchImprovement", "BenchFindings", "BenchMonitorCerts"] })
	.injectEndpoints({
		endpoints: (builder) => ({
			archiveObject: builder.mutation<ArchiveResponse | undefined, ArchiveArgs>({
				query: ({ kind, id }) => ({ url: archivePath(kind, id), method: "POST" }),
				invalidatesTags: (_r, _e, { kind, id }) => [...archiveTags(kind, id), "AuditLog"],
			}),
			unarchiveObject: builder.mutation<ArchiveResponse | undefined, ArchiveArgs>({
				query: ({ kind, id }) => ({ url: archivePath(kind, id, true), method: "POST" }),
				invalidatesTags: (_r, _e, { kind, id }) => [...archiveTags(kind, id), "AuditLog"],
			}),
			// GET /findings with the archive parameter, for the findings tab's "Show archived" switch.
			// Same LIST tag as findingsApi.getFindings, so an archive refreshes both.
			getFindingsWithArchived: builder.query<FindingsListResponse, { state?: FindingState; archived?: ArchivedParam }>({
				query: ({ state, archived }) => ({ url: "/findings", params: { ...(state ? { state } : {}), ...(archived ? { archived } : {}) } }),
				providesTags: [{ type: "BenchFindings", id: "LIST" }],
			}),
			// DELETE /monitors/certifications/{id} (admins): the folder of one monitor test goes.
			deleteMonitorTest: builder.mutation<unknown, string>({
				query: (certId) => ({ url: `/monitors/certifications/${encodeURIComponent(certId)}`, method: "DELETE" }),
				invalidatesTags: ["AuditLog"],
			}),
		}),
	});

export const { useArchiveObjectMutation, useUnarchiveObjectMutation, useDeleteMonitorTestMutation, useGetFindingsWithArchivedQuery } = archiveApi;
