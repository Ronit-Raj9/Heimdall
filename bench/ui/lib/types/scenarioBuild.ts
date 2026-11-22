// Scenario rows and OpenAPI mocks from the website (lane Y14: handlers/scenariorows.go,
// handlers/suitemocks.go).
import type { JudgeConfig, RowProblem } from "./suiteBuild";

/** Body of POST /suites/{id}/scenario-rows. */
export interface ScenarioRowsRequest {
	format: "csv" | "jsonl";
	data: string;
	/** The task whose services, instruction and seed the new tasks copy; empty takes the first with a simulated customer. */
	shape_task?: string;
	/** Move the benchmark to grader checks; its other tasks keep their verifier. */
	switch_grader?: boolean;
	judge?: JudgeConfig;
	held_out?: boolean;
	dry_run?: boolean;
	source?: string;
}

export interface ScenarioTask {
	line: number;
	id: string;
	title: string;
	held_out?: boolean;
	opening: string;
	check: string;
	checks: number;
	warnings?: string[];
}

/** Answer of POST /suites/{id}/scenario-rows. */
export interface ScenarioRowsResult {
	suite: string;
	shape: string;
	grader: string;
	grader_changed: boolean;
	dry_run: boolean;
	tasks: ScenarioTask[];
	problems: RowProblem[];
	next: string;
}

/** Body of POST /suites/{id}/mocks. */
export interface SuiteMockRequest {
	id: string;
	spec: string;
	file_name?: string;
	replace?: boolean;
	dry_run?: boolean;
}

export interface MockOperation {
	name: string;
	method: string;
	path: string;
	kind: "list" | "get" | "create" | "update" | "delete" | "action";
	collection?: string;
	description?: string;
}

/** Answer of POST /suites/{id}/mocks. */
export interface SuiteMockResult {
	suite: string;
	id: string;
	folder: string;
	dry_run: boolean;
	replaced: boolean;
	operations: MockOperation[];
	collections: string[];
	warnings: string[];
	service_yaml: string;
	seed: string;
	next: string;
}
