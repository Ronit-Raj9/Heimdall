/** Web parity L24 uploads: answers of POST /uploads/runs, /uploads/project and /uploads/agent. */

export type UploadKind = "runs" | "project" | "agent";

export interface ImportedExperiment {
	exp_id: string;
	runs: number;
	status: "imported" | "skipped" | "refused";
	reason?: string;
}

export interface UploadRunsReport {
	experiments: ImportedExperiment[] | null;
	imported: number;
	skipped: number;
	refused: number;
	runs: number;
}

export interface UploadedFile {
	area: string;
	path: string;
	size?: number;
	error?: string;
}

export interface UploadFilesReport {
	written: UploadedFile[];
	refused: UploadedFile[];
	skipped: string[];
}

export type UploadResult =
	| { kind: "runs"; report: UploadRunsReport }
	| { kind: "project" | "agent"; report: UploadFilesReport };
