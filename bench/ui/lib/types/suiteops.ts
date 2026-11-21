import type { TaskQuarantine } from "./bench";

// Suite ops area (web parity lane L11, handlers/suiteops.go and cmd/bifrost-bench/jobs_suiteops.go).

/** Body of POST /suites/{id}/tasks/{task}/quarantine. */
export interface QuarantineRequest {
	suite: string;
	task: string;
	reason: string;
}

/** Answer of the quarantine and restore routes. */
export interface QuarantineResult {
	suite: string;
	task: string;
	quarantine?: TaskQuarantine;
	/** False when restore found the task not quarantined. */
	changed: boolean;
}

/** Where a task is cut from: a GitHub owner/name (any mode) or a local folder (local mode). */
export interface RepoSource {
	github?: string;
	branch?: string;
	repo?: string;
}

/** Params of the task.from-commit job kind. */
export interface TaskFromCommitParams extends RepoSource {
	suite: string;
	id: string;
	base: string;
	reference: string;
	tests?: string[];
	license?: string;
	difficulty?: "" | "easy" | "medium" | "hard";
	kind?: "" | "bugfix" | "feature" | "refactor" | "performance";
	force?: boolean;
}

/** Params of the tasks.mine job kind. */
export interface TasksMineParams extends RepoSource {
	suite: string;
	since?: string;
	paths?: string[];
	limit?: number;
	min_tests?: number;
	license?: string;
	dry_run?: boolean;
}

/** Job kinds of the suite ops tab. */
export const SUITE_OPS_KINDS = {
	lint: "suite.lint",
	lock: "suite.lock",
	admit: "suite.admit",
	quarantineFlaky: "suite.quarantine-flaky",
	fromCommit: "task.from-commit",
	mine: "tasks.mine",
} as const;
