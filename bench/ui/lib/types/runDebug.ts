// X20b run debugging (handlers/rundebug.go, schemas/debug.go).

export type DebugMode = "replay" | "fork" | "step";
export type ReplayLabel = "hit" | "divergence" | "edited" | "exhausted";
export type HoldAction = "continue" | "step" | "edit" | "abort";
export type DivergencePolicy = "stop" | "live" | "serve";

/** run.json debug: where a debug run came from (defined beside the run in bench.ts). */
import type { RunDebugInfo } from "./bench";
export type RunDebug = RunDebugInfo;

export interface Breakpoints {
	calls?: number[];
	every?: boolean;
	after_tools?: string[];
}

/** One model call (GET /runs/{id}/calls). */
export interface RunCall {
	call: number;
	request_id?: string;
	status: number;
	action: string;
	stream?: boolean;
	replay?: ReplayLabel;
	replay_expected?: string;
	held_ms?: number;
	operator?: string;
}

export interface Hold {
	slot: string;
	run_id: string;
	call: number;
	since: string;
	deadline: string;
	reason: string;
	model?: string;
	stream?: boolean;
	request?: string;
	clipped?: boolean;
}

export interface HoldStatus {
	run_id: string;
	holds: Hold[];
	held_ms: number;
	stopped?: boolean;
}

export interface RunCalls {
	run_id: string;
	calls: RunCall[];
	debug?: RunDebug;
	first_divergence?: RunCall;
	holds?: HoldStatus;
}

/** GET /runs/{id}/calls/{request}. */
export interface RunCallDetail {
	run_id: string;
	request_id: string;
	call?: number;
	model?: string;
	stream?: boolean;
	status: number;
	request: string;
	response: string;
	clipped?: boolean;
	available: boolean;
}

export interface DebugRunRequest {
	call?: number;
	response?: string;
	message?: string;
	on_divergence?: DivergencePolicy;
	breakpoints?: Breakpoints;
	/** Spend cap of a fork or step run in USD (required there); a replay takes none. */
	budget_usd?: number;
}

export interface DebugRunResponse {
	exp_id: string;
	mode: DebugMode;
	from_run: string;
	started: boolean;
}

export interface HoldRelease {
	call: number;
	action: HoldAction;
	response?: string;
	message?: string;
}