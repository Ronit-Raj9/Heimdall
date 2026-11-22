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
}

export interface RowTask {
	line: number;
	id: string;
	title: string;
	held_out?: boolean;
	question: string;
	check: string;
	/** Wrong answers the readiness check grades (previews). */
	wrong_answers?: string[];
	/** The import wrote the wrong answers because the row named none (judged rows). */
	generated_wrong?: boolean;
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
