// Own benchmarks from the website (lane X13, handlers/suitebuild.go).

export type RowFormat = "csv" | "jsonl" | "tickets";
export type AnswerMatch = "exact" | "contains_all" | "regex" | "numeric" | "choice";
/** answer_match: exact answer, no model. judge: rubric judge. */
export type AnswerGrader = "answer_match" | "judge";

/** Body of POST /suites/{id}/rows. */
export interface RowImportRequest {
	format: RowFormat;
	data: string;
	match?: AnswerMatch;
	held_out?: boolean;
	dry_run?: boolean;
	source?: string;
	/** Which column feeds which task field; an empty field is detected from the column names. */
	mapping?: RowMapping;
	/** Store the mapping in the benchmark so the next import without one reuses it. */
	save_mapping?: boolean;
	/** Conversation rows: grade the last reply or the whole conversation. */
	grade_on?: GradeOn;
	/** Files the rows name in their attachments column. */
	attachments?: Record<string, RowAttachment>;
	/** Import in the background (always so above 500 rows). */
	async?: boolean;
}

export type GradeOn = "final" | "transcript";

/** Task fields a column can feed. */
export type MapField =
	| "id"
	| "title"
	| "question"
	| "messages"
	| "expected"
	| "rubric"
	| "reference"
	| "wrong_answers"
	| "match"
	| "category"
	| "split"
	| "user_turns"
	| "attachments"
	| "grade_on";

/**
 * Column path per field (dotted into nested records, "-" means do not read), plus the columns
 * copied into task metadata.
 */
export type RowMapping = Partial<Record<MapField, string>> & { metadata?: string[] };

export interface RowAttachment {
	content_base64: string;
	media_type?: string;
}

export type ColumnKind = "text" | "number" | "list" | "messages" | "object";

export interface RowColumn {
	path: string;
	kind: ColumnKind;
	example?: string;
	filled: number;
}

export interface RowImportStatus {
	id: string;
	suite: string;
	status: "running" | "done" | "failed" | "interrupted";
	source: string;
	rows: number;
	total: number;
	written: number;
	problem_count: number;
	problems: RowProblem[];
	error?: string;
	started_at: string;
	finished_at?: string;
	next?: string;
}

export interface RowTask {
	line: number;
	id: string;
	title: string;
	held_out?: boolean;
	question: string;
	check: string;
	/** User messages the agent answers. */
	turns?: number;
	attachments?: number;
	grade_on?: GradeOn;
	metadata?: Record<string, string>;
}

export interface RowProblem {
	line: number;
	message: string;
}

/** Answer of POST /suites/{id}/rows. */
export interface RowImportResult {
	suite: string;
	grader: AnswerGrader;
	dry_run: boolean;
	tasks: RowTask[];
	problems: RowProblem[];
	next: string;
	rows?: number;
	tasks_total?: number;
	columns?: RowColumn[];
	mapping?: RowMapping;
	mapping_saved?: boolean;
	/** Set when the import runs in the background (HTTP 202). */
	import?: RowImportStatus;
}

export interface JudgeConfig {
	provider: string;
	model: string;
	prompt_version?: string;
	votes?: number;
}

/** Body of PUT /suites/{id}/grader. */
export interface SuiteGraderRequest {
	grader: AnswerGrader;
	judge?: JudgeConfig;
}

export interface SuiteGraderResult {
	suite: string;
	grader: string;
	grading: string;
	judge?: JudgeConfig;
	next: string;
}

export interface TaskSplitResult {
	suite: string;
	task: string;
	split: "public" | "held_out";
}
