import type { Mode } from "@/lib/types/bench";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		schedules: typeof import("@/locales/en/schedules.json");
	}
}

/** One webhook receiver. url_env / secret_env name an org secret (or, locally, an env variable). */
export interface AlertConfig {
	url_env: string;
	secret_env?: string;
	events?: string[];
	format?: "" | "json" | "slack";
	timeout_s?: number;
}

/** One schedule of policies/schedules.yaml (bench/schedule.Schedule). */
export interface Schedule {
	name: string;
	experiment?: string;
	/** An evaluation to repeat: a saved evaluation id (ev_...) or a spec file; in place of experiment. */
	evaluation?: string;
	cron: string;
	timezone?: string;
	policy?: string;
	max_usd_per_day?: number;
	fail_on?: string[];
	alerts?: AlertConfig[];
	job?: string;
	paused?: boolean;
}

export interface SchedulesFile {
	alerts?: AlertConfig[];
	schedules: Schedule[];
}

export interface ScheduleRecord {
	at: string;
	started: string;
	finished: string;
	schedule: string;
	status: string;
	reason?: string;
	spent_today_usd: number;
	alerts?: string[];
}

export interface ScheduleRow extends Schedule {
	kind: "experiment" | "evaluation" | "canary" | "job";
	source: "web" | "canary";
	next?: string;
	last?: ScheduleRecord;
	problem?: string;
	spent_today_usd: number;
}

export interface SchedulesResponse {
	mode?: Mode;
	file: SchedulesFile;
	yaml: string;
	rows: ScheduleRow[];
	events: string[];
	secrets: string[];
	hosted: boolean;
	ticker: boolean;
	/** Hosted guardrails: at most max_schedules per org, each firing at most every min_interval_min minutes. */
	limits?: { max_schedules: number; min_interval_min: number };
}

export interface TestAlertRequest {
	url_env: string;
	secret_env?: string;
	format?: string;
}

export interface TestAlertResponse {
	delivered: boolean;
	error?: string;
}

export interface ScheduleRunResponse {
	job_id: string;
	kind: string;
}

/** POST /canaries body (bench/canary.Definition plus force). */
export interface CanaryInitRequest {
	name: string;
	description?: string;
	agent: string;
	suite: string;
	tasks: "all" | string[];
	trials: number;
	targets: { provider?: string; model: string }[];
	schedule?: { cron: string; timezone?: string };
	budget: { per_run_usd?: number; per_day_usd?: number };
	smoke?: boolean;
	force?: boolean;
}

export interface CanaryInitResponse {
	name: string;
	path: string;
	next: string;
}