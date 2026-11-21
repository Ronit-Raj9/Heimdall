import { createFileRoute } from "@tanstack/react-router";
import FilesPage from "./page";

/** Deep links: /files?area=agents&dir=sub&path=sub/x.yaml opens that folder and file. */
export interface FilesSearch {
	area?: string;
	dir?: string;
	path?: string;
}

export const Route = createFileRoute("/files")({
	validateSearch: (search: Record<string, unknown>): FilesSearch => ({
		...(typeof search.area === "string" ? { area: search.area } : {}),
		...(typeof search.dir === "string" ? { dir: search.dir } : {}),
		...(typeof search.path === "string" ? { path: search.path } : {}),
	}),
	component: FilesPage,
});
