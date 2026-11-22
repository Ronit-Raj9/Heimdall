// Project files area (handlers/projectfiles.go).

export type ProjectArea = "agents" | "benchmarks" | "experiments" | "policies" | "uploads";

export const PROJECT_AREAS: readonly ProjectArea[] = ["agents", "benchmarks", "experiments", "policies", "uploads"];

export interface ProjectEntry {
	path: string;
	size: number;
	dir: boolean;
	mod_time: string;
	sha256?: string;
}

export interface ProjectFileList {
	area?: string;
	dir: string;
	entries: ProjectEntry[];
}

export interface ProjectFile {
	area: string;
	path: string;
	entry: ProjectEntry;
	encoding: "utf-8" | "base64";
	content: string;
}

export interface ProjectFileSaved {
	area: string;
	path: string;
	entry?: ProjectEntry;
	deleted?: boolean;
}

export interface ProjectVersion {
	id: string;
	path: string;
	sha256: string;
	actor?: string;
	/** How actor is shown; an email actor is sent to org admins only. */
	actor_name?: string;
	note?: string;
	size: number;
	at: string;
}

export interface ProjectFileVersions {
	area: string;
	path: string;
	versions: ProjectVersion[];
}
