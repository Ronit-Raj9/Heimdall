import type { ProjectArea, ProjectFile, ProjectFileList, ProjectFileSaved, ProjectFileVersions } from "@/lib/types/projectfiles";
import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/**
 * Project files (web parity L01): list, read, save, delete and version the org's project folder.
 * Saves send the file's bytes as the body with If-Match set to the sha256 the editor loaded, so a
 * concurrent edit answers 409 instead of being overwritten.
 */
export const projectFilesApi = benchApi.enhanceEndpoints({ addTagTypes: ["ProjectFiles"] }).injectEndpoints({
	endpoints: (builder) => ({
		listProjectFiles: builder.query<ProjectFileList, { area?: ProjectArea; dir?: string; recursive?: boolean }>({
			query: ({ area, dir, recursive }) => projectListPath(area, dir, recursive),
			providesTags: [{ type: "ProjectFiles", id: "LIST" }],
		}),
		getProjectFile: builder.query<ProjectFile, { area: ProjectArea; path: string }>({
			query: ({ area, path }) => projectFilePath(area, path),
			providesTags: (_r, _e, { area, path }) => [{ type: "ProjectFiles", id: `${area}/${path}` }],
		}),
		saveProjectFile: builder.mutation<ProjectFileSaved, SaveProjectFileArgs>({
			query: (args) => saveProjectFileRequest(args),
			invalidatesTags: (_r, _e, { area, path }) => [
				{ type: "ProjectFiles", id: "LIST" },
				{ type: "ProjectFiles", id: `${area}/${path}` },
				{ type: "ProjectFiles", id: `versions:${area}/${path}` },
			],
		}),
		deleteProjectFile: builder.mutation<ProjectFileSaved, { area: ProjectArea; path: string; ifMatch?: string }>({
			query: ({ area, path, ifMatch }) => ({
				url: projectFilePath(area, path),
				method: "DELETE",
				...(ifMatch ? { headers: { "If-Match": `"${ifMatch}"` } } : {}),
			}),
			invalidatesTags: (_r, _e, { area, path }) => [
				{ type: "ProjectFiles", id: "LIST" },
				{ type: "ProjectFiles", id: `${area}/${path}` },
				{ type: "ProjectFiles", id: `versions:${area}/${path}` },
			],
		}),
		getProjectFileVersions: builder.query<ProjectFileVersions, { area: ProjectArea; path: string }>({
			query: ({ area, path }) => projectVersionsPath(area, path),
			providesTags: (_r, _e, { area, path }) => [{ type: "ProjectFiles", id: `versions:${area}/${path}` }],
		}),
		getProjectFileVersion: builder.query<ProjectFile, { area: ProjectArea; path: string; id: string }>({
			query: ({ area, path, id }) => `${projectVersionsPath(area, path)}?id=${encodeURIComponent(id)}`,
		}),
	}),
});

export interface SaveProjectFileArgs {
	area: ProjectArea;
	path: string;
	content: string;
	/** sha256 of the content the editor loaded; leave out for a new file. */
	ifMatch?: string;
	note?: string;
}

/** Escapes each path segment, keeping the slashes. */
export function encodeProjectPath(path: string): string {
	return path
		.split("/")
		.filter((part) => part.length > 0)
		.map(encodeURIComponent)
		.join("/");
}

export function projectListPath(area?: ProjectArea, dir?: string, recursive?: boolean): string {
	const q = new URLSearchParams();
	if (area) q.set("area", area);
	if (dir) q.set("dir", dir);
	if (recursive) q.set("recursive", "true");
	const qs = q.toString();
	return qs ? `/project/files?${qs}` : "/project/files";
}

export function projectFilePath(area: ProjectArea, path: string): string {
	return `/project/files/${area}/${encodeProjectPath(path)}`;
}

export function projectVersionsPath(area: ProjectArea, path: string): string {
	return `/project/versions/${area}/${encodeProjectPath(path)}`;
}

/** PUT /project/files/{area}/{path} with the raw content as the body. */
export function saveProjectFileRequest({ area, path, content, ifMatch, note }: SaveProjectFileArgs): FetchArgs {
	const headers: Record<string, string> = { "Content-Type": "text/plain; charset=utf-8" };
	if (ifMatch) headers["If-Match"] = `"${ifMatch}"`;
	return {
		url: projectFilePath(area, path) + (note ? `?note=${encodeURIComponent(note)}` : ""),
		method: "PUT",
		body: content,
		headers,
	};
}

/** The download URL of the whole project as a zip. */
export function projectExportUrl(): string {
	return `${BENCH_API_BASE}/project/export.zip`;
}

/** The raw download URL of one file, any size (the blobs route streams it). */
export function projectRawUrl(area: ProjectArea, path: string): string {
	return `${BENCH_API_BASE}/project/blobs/${area}/${encodeProjectPath(path)}`;
}

export const {
	useListProjectFilesQuery,
	useGetProjectFileQuery,
	useSaveProjectFileMutation,
	useDeleteProjectFileMutation,
	useGetProjectFileVersionsQuery,
	useLazyGetProjectFileVersionQuery,
} = projectFilesApi;
