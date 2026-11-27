import type { UploadFilesReport, UploadRunsReport } from "@/lib/types/upload";
import { transportErrorText } from "@/lib/requestErrors";
import { benchApi } from "./benchApi";

/** The upload size limit the server and Caddy enforce (lib.MaxUploadBytes). */
export const MAX_UPLOAD_BYTES = 64 << 20;

/**
 * Web parity L24: zip uploads. The zip is the raw request body (application/zip), so no
 * multipart encoding is needed and the shared base query adds auth, CSRF and org headers.
 */
export const uploadApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		uploadRuns: builder.mutation<UploadRunsReport, File>({
			query: (file) => ({ url: "/uploads/runs", method: "POST", body: file, headers: { "Content-Type": "application/zip" } }),
			invalidatesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
		uploadProject: builder.mutation<UploadFilesReport, File>({
			query: (file) => ({ url: "/uploads/project", method: "POST", body: file, headers: { "Content-Type": "application/zip" } }),
		}),
		uploadAgent: builder.mutation<UploadFilesReport, { file: File; name?: string }>({
			query: ({ file, name }) => ({
				url: name ? `/uploads/agent?name=${encodeURIComponent(name)}` : "/uploads/agent",
				method: "POST",
				body: file,
				headers: { "Content-Type": "application/zip" },
			}),
		}),
	}),
});

export const { useUploadRunsMutation, useUploadProjectMutation, useUploadAgentMutation } = uploadApi;

/** The server's error message from an RTK Query error, or a fallback. */
export function uploadErrorMessage(error: unknown, fallback: string): string {
	const transport = transportErrorText(error);
	if (transport) return transport;
	const e = error as { status?: unknown; data?: { error?: { message?: string } } } | undefined;
	return e?.data?.error?.message ?? fallback;
}