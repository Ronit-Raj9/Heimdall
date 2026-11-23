import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import type { RunControls } from "@/lib/store/apis/runControlApi";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { EvaluationRunActions, PauseControls, RunActions, RunControlsPanel } from "./runControls";

const api = vi.hoisted(() => ({ controls: undefined as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/runControlApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/runControlApi")>()),
	useGetRunControlsQuery: () => ({ data: api.controls }),
	usePauseExperimentMutation: () => mutation({}),
	useUnpauseExperimentMutation: () => mutation({}),
	usePatchRunControlsMutation: () => mutation({}),
	useCancelRunMutation: () => mutation({}),
	useRequeueRunMutation: () => mutation({}),
	useRerunFailuresMutation: () => mutation({}),
	usePauseEvaluationMutation: () => mutation({}),
	useResumeEvaluationMutation: () => mutation({}),
	useRerunEvaluationFailuresMutation: () => mutation({}),
}));

const controls = (over: Partial<RunControls> = {}): RunControls => ({
	exp_id: "e_1",
	state: { mode: "running", seq: 1 },
	paused: false,
	in_flight: 1,
	models: ["claude-sonnet"],
	concurrency: 2,
	total_usd: 10,
	policy: [
		{
			class: "aborted_infra",
			retry: "auto",
			max_attempts: 3,
			uses_attempt: true,
			grade: "not_graded",
			counts: false,
			editable: true,
			note: "",
		},
		{
			class: "infra_error",
			retry: "manual",
			max_attempts: 0,
			uses_attempt: false,
			grade: "unknown",
			counts: true,
			editable: true,
			note: "",
		},
	],
	log: [
		{ seq: 1, at: "2026-11-21T10:00:00Z", actor: "ana", action: "pause", reason: "outage", before: {}, after: {}, prev: "", hash: "h" },
	],
	log_entries: 1,
	log_verified: true,
	exploratory: false,
	...over,
});

beforeEach(() => {
	api.controls = undefined;
});

describe("RunControlsPanel", () => {
	it("shows live settings while running, the policy table and the verified log", () => {
		api.controls = controls();
		const html = render(<RunControlsPanel expId="e_1" state="running" />, hostedSession("member"));
		expect(html).toContain('data-testid="live-settings"');
		expect(html).toContain('data-testid="policy-row-aborted_infra"');
		expect(html).toContain("Never reached the agent");
		expect(html).toContain("by requeue");
		expect(html).toContain("1 changes, verified");
		expect(html).toContain("outage");
		expect(html).not.toContain("rerun-failures-button");
	});
	it("offers Rerun failures once finished, never on an exploratory rerun", () => {
		api.controls = controls();
		expect(render(<RunControlsPanel expId="e_1" state="done" />, hostedSession("member"))).toContain("rerun-failures-button");
		api.controls = controls({ exploratory: true, rerun_of: "e_0" });
		const html = render(<RunControlsPanel expId="e_1" state="done" />, hostedSession("member"));
		expect(html).toContain('data-testid="exploratory-banner"');
		expect(html).not.toContain("rerun-failures-button");
		expect(html).not.toContain('data-testid="live-settings"');
	});
	it("flags a change log that does not verify", () => {
		api.controls = controls({ log_verified: false, log_problem: "entry 2 hash mismatch" });
		expect(render(<RunControlsPanel expId="e_1" state="running" />, hostedSession("member"))).toContain(
			"does not verify: entry 2 hash mismatch",
		);
	});
	it("hides settings from viewers", () => {
		api.controls = controls();
		expect(render(<RunControlsPanel expId="e_1" state="running" />, hostedSession("viewer"))).not.toContain('data-testid="live-settings"');
	});
});

describe("RunActions", () => {
	it("cancels a run in flight and requeues only aborted_infra or infra_error", () => {
		const m = hostedSession("member");
		expect(render(<RunActions expId="e_1" runId="r_1" sealed={false} />, m)).toContain('data-testid="run-cancel"');
		expect(render(<RunActions expId="e_1" runId="r_1" sealed outcome="infra_error" />, m)).toContain('data-testid="run-requeue"');
		expect(render(<RunActions expId="e_1" runId="r_1" sealed outcome="aborted_infra" />, m)).toContain('data-testid="run-requeue"');
		expect(render(<RunActions expId="e_1" runId="r_1" sealed outcome="fail" />, m)).toBe("");
		expect(render(<RunActions expId="e_1" runId="r_1" sealed outcome="infra_error" superseded />, m)).toContain("Replaced by a requeue");
	});
});

describe("EvaluationRunActions", () => {
	const ev = (status: string, extra: object = {}) =>
		({
			id: "ev_1",
			status,
			spec: { name: "n", mode: "score", arms: [], benchmarks: [], tasks_per_benchmark: 8, trials: 2, faults: [] },
			children: [],
			created_at: "",
			...extra,
		}) as never;
	it("pauses a running evaluation, resumes a paused one, reruns failures of a finished one", () => {
		const m = hostedSession("member");
		expect(render(<EvaluationRunActions evaluation={ev("running")} />, m)).toContain('data-testid="evaluation-pause"');
		expect(render(<EvaluationRunActions evaluation={ev("running", { paused: true })} />, m)).toContain('data-testid="evaluation-unpause"');
		expect(render(<EvaluationRunActions evaluation={ev("done", { failed_runs: 2 })} />, m)).toContain(
			'data-testid="evaluation-rerun-failures"',
		);
		expect(render(<EvaluationRunActions evaluation={ev("done", { rerun_of: "ev_0", failed_runs: 2 })} />, m)).not.toContain(
			"evaluation-rerun-failures",
		);
	});
});

describe("run control names (J6.log-user)", () => {
	it("prints the actor's name in the change log and keeps the id in a title", () => {
		api.controls = {
			...controls({
				log: [{ seq: 1, at: "2026-11-21T10:00:00Z", actor: "usr_01m7", action: "pause", before: {}, after: {}, prev: "", hash: "h" }],
			}),
			actor_names: { usr_01m7: "Ana Lima" },
		};
		const html = render(<RunControlsPanel expId="e_1" state="running" />, hostedSession("member"));
		expect(html).toMatch(/title="usr_01m7"[^>]*>Ana Lima</);
	});
	it("keeps the id when the server sent no name", () => {
		api.controls = controls({
			log: [{ seq: 1, at: "2026-11-21T10:00:00Z", actor: "usr_01m7", action: "pause", before: {}, after: {}, prev: "", hash: "h" }],
		});
		expect(render(<RunControlsPanel expId="e_1" state="running" />, hostedSession("member"))).toMatch(/title="usr_01m7"[^>]*>usr_01m7</);
	});
	it("names who paused on the paused chip", () => {
		api.controls = { ...controls({ paused: true, state: { mode: "paused", seq: 2, paused_by: "usr_01m7" } }), paused_by_name: "Ana Lima" };
		const html = render(<PauseControls expId="e_1" />, hostedSession("member"));
		expect(html).toContain('title="Paused by Ana Lima"');
		expect(html).not.toContain("Paused by usr_01m7");
	});
});
describe("Rerun failures only where it can work (fix wave 3, N183 and N285)", () => {
	const ev = (status: string, extra: object = {}) =>
		({
			id: "ev_1",
			status,
			spec: { name: "n", mode: "score", arms: [], benchmarks: [], tasks_per_benchmark: 8, trials: 2, faults: [] },
			children: [],
			created_at: "",
			...extra,
		}) as never;
	it("hides Rerun failures on an evaluation with no failed run (N183)", () => {
		const m = hostedSession("member");
		expect(render(<EvaluationRunActions evaluation={ev("cancelled", { failed_runs: 0 })} />, m)).not.toContain("evaluation-rerun-failures");
		expect(render(<EvaluationRunActions evaluation={ev("cancelled")} />, m)).not.toContain("evaluation-rerun-failures");
		expect(render(<EvaluationRunActions evaluation={ev("done", { failed_runs: 2 })} />, m)).toContain("evaluation-rerun-failures");
	});
	it("keeps the rerun notice out of the actions row (N193)", () => {
		const html = render(<EvaluationRunActions evaluation={ev("done", { failed_runs: 2 })} />, hostedSession("member"));
		expect(html).not.toContain("evaluation-rerun-started");
	});
	it("shows the server's reason instead of the button when a rerun cannot work (N285)", () => {
		const reason =
			"Rerun failures works on a comparison of two setups. For this comparison of 3 models, start a new comparison with the failed tasks.";
		api.controls = { ...controls(), rerun_failures: { allowed: false, reason } };
		const html = render(<RunControlsPanel expId="e_1" state="done" />, hostedSession("member"));
		expect(html).not.toContain("rerun-failures-button");
		expect(html).toContain(reason);
		expect(html).toContain('data-testid="rerun-failures-reason"');
		api.controls = { ...controls(), rerun_failures: { allowed: true } };
		expect(render(<RunControlsPanel expId="e_1" state="done" />, hostedSession("member"))).toContain("rerun-failures-button");
	});
});

describe("change log in plain words (fix wave 3, N199)", () => {
	const entry = (before: object, after: object) => ({
		seq: 2,
		at: "2026-11-21T10:00:00Z",
		actor: "ana",
		action: "set",
		before,
		after,
		prev: "",
		hash: "h",
	});
	const logOf = (before: object, after: object) => {
		api.controls = controls({ log: [entry(before, after)] });
		return render(<RunControlsPanel expId="e_1" state="running" />, hostedSession("member"));
	};
	it("names the held and released model", () => {
		expect(logOf({ mode: "running" }, { mode: "running", paused_models: ["gpt-5.4-mini"] })).toContain("held gpt-5.4-mini");
		expect(logOf({ mode: "running", paused_models: ["gpt-5.4-mini"] }, { mode: "running" })).toContain("released gpt-5.4-mini");
	});
	it("names the concurrency and spend cap change", () => {
		expect(logOf({ mode: "running", concurrency: 2 }, { mode: "running", concurrency: 4 })).toContain("set runs at once from 2 to 4");
		const cap = logOf({ mode: "running", total_usd: 0.005 }, { mode: "running", total_usd: 2 });
		expect(cap).toContain("set the spend cap from $0.0050 to $2.00");
		expect(logOf({ mode: "running" }, { mode: "running" })).toContain("changed settings");
	});
	it("joins several changes", () => {
		expect(logOf({ mode: "running", concurrency: 2 }, { mode: "running", concurrency: 3, paused_models: ["a"] })).toContain(
			"held a; set runs at once from 2 to 3",
		);
	});
});