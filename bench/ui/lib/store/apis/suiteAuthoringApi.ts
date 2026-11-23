import type {
	CreateSuiteRequest,
	CreateSuiteResponse,
	CreateTaskRequest,
	CreateTaskResponse,
	SuiteFile,
	SuiteFileList,
	SuiteLintReport,
	SuiteTemplateList,
	SuiteUploadResult,
} from "@/lib/types/suiteAuthoring";
import { benchApi } from "./benchApi";

const seg = (v: string) => encodeURIComponent(v);
/** A path inside a suite, each segment escaped. */
export const filePath = (p: string) => p.split("/").map(seg).join("/");

/** Suite authoring (web parity L10): create suites and tasks, edit and upload files, lint. */
export const suiteAuthoringApi = benchApi.enhanceEndpoints({ addTagTypes: ["SuiteAuthoring"] }).injectEndpoints({
	endpoints: (builder) => ({
		getSuiteTemplates: builder.query<SuiteTemplateList, void>({ query: () => "/suite-templates" }),
		createSuite: builder.mutation<CreateSuiteResponse, CreateSuiteRequest>({
			query: (body) => ({ url: "/suites", method: "POST", body }),
			invalidatesTags: [{ type: "BenchSuites", id: "LIST" }],
		}),
		createSuiteTask: builder.mutation<CreateTaskResponse, { suite: string } & CreateTaskRequest>({
			query: ({ suite, ...body }) => ({ url: `/suites/${seg(suite)}/tasks`, method: "POST", body }),
			invalidatesTags: (_r, _e, { suite }) => [
				{ type: "BenchSuites", id: "LIST" },
				{ type: "SuiteAuthoring", id: suite },
			],
		}),
		getSuiteFiles: builder.query<SuiteFileList, string>({
			query: (suite) => `/suites/${seg(suite)}/files`,
			providesTags: (_r, _e, suite) => [{ type: "SuiteAuthoring", id: suite }],
		}),
		getSuiteFile: builder.query<SuiteFile, { suite: string; path: string }>({
			query: ({ suite, path }) => `/suites/${seg(suite)}/files/${filePath(path)}`,
			providesTags: (_r, _e, { suite, path }) => [{ type: "SuiteAuthoring", id: `${suite}/${path}` }],
		}),
		putSuiteFile: builder.mutation<unknown, { suite: string; path: string; content: string | Blob; ifMatch?: string }>({
			query: ({ suite, path, content, ifMatch }) => ({
				url: `/suites/${seg(suite)}/files/${filePath(path)}`,
				method: "PUT",
				body: content,
				headers: { "Content-Type": "application/octet-stream", ...(ifMatch ? { "If-Match": ifMatch } : {}) },
			}),
			invalidatesTags: (_r, _e, { suite, path }) => [
				{ type: "SuiteAuthoring", id: suite },
				{ type: "SuiteAuthoring", id: `${suite}/${path}` },
				{ type: "SuiteAuthoring", id: `${suite}/lint` },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
		deleteSuiteFile: builder.mutation<unknown, { suite: string; path: string }>({
			query: ({ suite, path }) => ({ url: `/suites/${seg(suite)}/files/${filePath(path)}`, method: "DELETE" }),
			invalidatesTags: (_r, _e, { suite }) => [
				{ type: "SuiteAuthoring", id: suite },
				{ type: "SuiteAuthoring", id: `${suite}/lint` },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
		uploadSuiteZip: builder.mutation<SuiteUploadResult, { suite: string; dir: string; file: Blob }>({
			query: ({ suite, dir, file }) => ({
				url: `/suites/${seg(suite)}/upload`,
				method: "POST",
				params: { dir },
				body: file,
				headers: { "Content-Type": "application/zip" },
			}),
			invalidatesTags: (_r, _e, { suite }) => [
				{ type: "SuiteAuthoring", id: suite },
				{ type: "SuiteAuthoring", id: `${suite}/lint` },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
		// Fix wave 3, N288: delete a benchmark of the organization (204); its comparisons and runs stay.
		deleteSuite: builder.mutation<void, string>({
			query: (suite) => ({ url: `/suites/${seg(suite)}`, method: "DELETE" }),
			invalidatesTags: (_r, _e, suite) => [
				{ type: "BenchSuites", id: "LIST" },
				{ type: "SuiteAuthoring", id: suite },
			],
		}),
		getSuiteLint: builder.query<SuiteLintReport, string>({
			query: (suite) => `/suites/${seg(suite)}/lint`,
			providesTags: (_r, _e, suite) => [{ type: "SuiteAuthoring", id: `${suite}/lint` }],
		}),
	}),
});

/** The server's error message of a failed request, or a fallback. */
export function authoringError(error: unknown, fallback: string): string {
	const e = error as { status?: unknown; data?: { error?: { message?: string } } } | undefined;
	return e?.data?.error?.message || (e?.status ? `${fallback} (${String(e.status)})` : fallback);
}

export const {
	useGetSuiteTemplatesQuery,
	useCreateSuiteMutation,
	useCreateSuiteTaskMutation,
	useGetSuiteFilesQuery,
	useGetSuiteFileQuery,
	usePutSuiteFileMutation,
	useDeleteSuiteFileMutation,
	useUploadSuiteZipMutation,
	useGetSuiteLintQuery,
	useDeleteSuiteMutation,
} = suiteAuthoringApi;