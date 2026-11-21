// Suite authoring area (web parity L10): handlers/suiteauthoring.go response shapes.

export interface SuiteTemplate {
	id: string;
	scenario: boolean;
	sample?: string;
}

export interface SuiteTemplateList {
	templates: SuiteTemplate[];
	default: string;
}

export interface CreateSuiteRequest {
	id: string;
	template: string;
	image?: string;
	/** Leave out a scenario template's sample task (the suite starts empty). */
	blank?: boolean;
}

export interface CreateSuiteResponse {
	id: string;
	template: string;
	files: string[];
	sample?: string;
	visible: boolean;
	next: string;
}

export interface CreateTaskRequest {
	id: string;
	title?: string;
	kind?: string;
	instruction?: string;
	license?: string;
}

export interface CreateTaskResponse {
	suite: string;
	task: string;
	template: string;
	files: string[];
	warnings: string[];
}

export interface SuiteFileEntry {
	path: string;
	size: number;
	dir: boolean;
	mod_time: string;
	sha256?: string;
}

export interface SuiteFileList {
	suite: string;
	files: SuiteFileEntry[];
	truncated: boolean;
}

export interface SuiteFile {
	path: string;
	size: number;
	sha256: string;
	binary: boolean;
	content?: string;
}

export interface SuiteLintProblem {
	path: string;
	message: string;
	severity: "error" | "warning";
}

export interface SuiteLintReport {
	suite: string;
	version: string;
	tasks: number;
	probes: number;
	problems: SuiteLintProblem[];
	errors: number;
	warnings: number;
	load_error?: string;
}

export interface SuiteUploadResult {
	suite: string;
	dir: string;
	files: SuiteFileEntry[];
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		suiteAuthoring: typeof import("@/locales/en/suiteAuthoring.json");
	}
}
