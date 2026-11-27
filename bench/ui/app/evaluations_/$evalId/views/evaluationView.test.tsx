import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/reportcard/evaluationReportCard", () => ({
	EvaluationReportCard: ({ evaluationId, evaluation }: { evaluationId: string; evaluation?: { id: string } }) => (
		<div data-testid="report-card-mock" data-evaluation={evaluation?.id ?? ""}>
			{evaluationId}
		</div>
	),
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<object>()),
	useGetExperimentQuery: vi.fn(() => okQuery({ exp_id: "e1", slots: { total: 20, done: 7, running: 2, failed: 0 } })),
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({
	...(await orig<object>()),
	useGetEvaluationQuery: vi.fn(),
	// Lane RB: the page reads the report (subtitle totals) and the release preview (summary line).
	useGetEvaluationReportQuery: vi.fn(() => ({ data: undefined })),
	useCancelEvaluationMutation: vi.fn(() => [() => ({ unwrap: () => Promise.resolve() }), { isLoading: false }]),
	useRaiseEvaluationBudgetMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
}));

vi.mock("@/components/releases/promoteEvaluation", () => ({
	PromoteEvaluation: ({ evaluationId }: { evaluationId: string }) => <div data-testid="promote-mock">{evaluationId}</div>,
	RepeatEvaluation: () => <div data-testid="repeat-mock" />,
	GateResultCard: ({ gate }: { gate: { state: string } }) => <div data-testid="gate-mock">{gate.state}</div>,
}));

vi.mock("@/lib/store/apis/evalReleaseApi", async (orig) => ({
	...(await orig<object>()),
	useGetEvaluationReleasePreviewQuery: vi.fn(() => ({ data: undefined })),
}));
vi.mock("@/lib/store/apis/dataOpsApi", () => ({ useDeleteEvaluationMutation: () => [() => ({ unwrap: () => Promise.resolve() }), {}] }));

vi.mock("@/lib/store/apis/runControlApi", async (orig) => ({
	...(await orig<object>()),
	usePauseEvaluationMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
	useResumeEvaluationMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
	useRerunEvaluationFailuresMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
}));

vi.mock("@/lib/store/apis/agentopsApi", async (orig) => ({
	...(await orig<object>()),
	useScanAgentSkillMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
}));

vi.mock("@/lib/store/apis/bifrostUseApi", async (orig) => ({
	...(await orig<object>()),
	useGetEvaluationBifrostUseQuery: vi.fn(() => ({ data: undefined })),
}));

const { EvaluationBody } = await import("./evaluationView");

const base: Evaluation = {
	id: "ev_1",
	spec: {
		name: "Codex v1 vs v2",
		mode: "versions",
		arms: [
			{ label: "A", agent: "codex", version: 1, model: "gpt-x" },
			{ label: "B", agent: "codex", version: 2, model: "gpt-x", provider: "openai" },
		],
		benchmarks: ["hd-harbor-tb2", "voicebench"],
		tasks_per_benchmark: 5,
		trials: 2,
		faults: ["clean", "model-down"],
	},
	status: "running",
	children: [
		{ benchmark: "hd-harbor-tb2", experiment_id: "e1", status: "running" },
		{ benchmark: "voicebench", experiment_id: "", status: "skipped", skipped: "agent codex cannot run benchmark voicebench: needs voice" },
	],
	created_at: "2026-11-20T10:00:00Z",
};

describe("EvaluationBody", () => {
	it("shows arms, mode in plain words, progress rows with links, skipped reason and Cancel while running", () => {
		const html = render(<EvaluationBody evaluation={base} />);
		expect(html).toContain("Compare two versions of one agent");
		expect(html).toContain('data-testid="evaluation-arm-A"');
		expect(html).toContain("openai/gpt-x");
		expect(html).toContain("v2");
		// The config card names the fault once, under Fault (M3-17).
		expect(html).toContain("model outage");
		expect(html).toContain("7 of 20 runs done");
		expect(html).toContain('href="/experiments/e1"');
		expect(html).toContain('href="/experiments/e1?tab=runs"');
		expect(html).toContain("needs voice");
		expect(html).toContain('data-testid="evaluation-cancel"');
		expect(html).not.toContain('data-testid="report-card-mock"');
	});

	it("renders the report card when done and hides Cancel", () => {
		const html = render(<EvaluationBody evaluation={{ ...base, status: "done", children: [{ ...base.children[0], status: "done" }] }} />);
		expect(html).toContain('data-testid="report-card-mock"');
		expect(html).not.toContain('data-testid="evaluation-cancel"');
		expect(html).toContain("Finished");
	});

	it("shows the gate result, Promote this version and Repeat on a finished evaluation", () => {
		const gate_result = {
			state: "fail",
			agent: "codex",
			candidate: "B",
			checks: [],
			summary: "Gate failed",
			decided_at: "2026-11-20T11:00:00Z",
		};
		const html = render(
			<EvaluationBody evaluation={{ ...base, status: "done", gate_result, children: [{ ...base.children[0], status: "done" }] }} />,
		);
		expect(html).toContain('data-testid="gate-mock"');
		expect(html).toContain('data-testid="promote-mock"');
		expect(html).toContain('data-testid="repeat-mock"');
	});

	it("offers no promotion while running", () => {
		const html = render(<EvaluationBody evaluation={base} />);
		expect(html).not.toContain('data-testid="promote-mock"');
		expect(html).not.toContain('data-testid="repeat-mock"');
	});
});

describe("EvaluationBody delete (X16)", () => {
	it("disables Delete while the evaluation runs and enables it once done", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		const { tagOf } = await import("@/lib/testing/render");
		const running = render(<EvaluationBody evaluation={base} />, hostedSession("admin"));
		expect(tagOf(running, "evaluation-delete")).toContain('disabled=""');
		expect(running).toContain("Cancel the evaluation or wait for it to finish before deleting it.");
		const done = render(<EvaluationBody evaluation={{ ...base, status: "done" }} />, hostedSession("admin"));
		expect(tagOf(done, "evaluation-delete")).not.toContain('disabled=""');
		expect(render(<EvaluationBody evaluation={{ ...base, status: "done" }} />, hostedSession("viewer"))).not.toContain(
			'data-testid="evaluation-delete"',
		);
	});
});

describe("EvaluationBody budget (G4-01)", () => {
	it("shows spend per benchmark, the runs the cap stopped and Raise budget and continue", () => {
		const html = render(
			<EvaluationBody
				evaluation={{
					...base,
					status: "done",
					children: [
						{
							benchmark: "hd-harbor-tb2",
							experiment_id: "e1",
							status: "failed",
							budget_usd: 5,
							spent_usd: 4.6,
							budget_stopped: true,
							not_started: 13,
						},
						{ benchmark: "tau2", experiment_id: "e2", status: "done", budget_usd: 5, spent_usd: 2.1 },
					],
				}}
			/>,
		);
		expect(html).toContain("Stopped by the budget: spent $4.60 of $5.00. 13 runs did not start.");
		expect(html).toContain("$2.10 of $5.00 spent");
		// A done evaluation's raise panel sits under the report card headline (M3-4): the page hands the report card the
		// evaluation and shows no second panel of its own. The headline and the panel are tested on the real card in
		// components/reportcard/evaluationReportCard.budget.test.tsx.
		expect(html).toContain('data-testid="report-card-mock" data-evaluation="ev_1"');
		expect(html.match(/data-testid="evaluation-budget-stopped"/g)).toBeNull();
	});

	it("shows no raise panel when nothing was stopped", () => {
		const html = render(<EvaluationBody evaluation={base} />);
		expect(html).not.toContain('data-testid="evaluation-budget-stopped"');
	});
});

describe("EvaluationBody (F08)", () => {
	it("lists each benchmark's reason in the empty note when none ran (D5.ui)", () => {
		const html = render(
			<EvaluationBody
				evaluation={{
					...base,
					status: "failed",
					children: [
						{
							benchmark: "hd-harbor-bfcl",
							experiment_id: "",
							status: "skipped",
							skipped: "plan experiment: the paired_signflip verdict needs arms A (baseline) and B (candidate); arm B is missing",
						},
					],
				}}
			/>,
		);
		expect(html).toContain("No benchmark in this evaluation could be run.");
		expect(html).toContain(
			"hd-harbor-bfcl: plan experiment: the paired_signflip verdict needs arms A (baseline) and B (candidate); arm B is missing",
		);
		expect(html).not.toContain('data-testid="evaluation-skipped"');
	});

	it("puts skipped benchmarks above the Bifrost section", () => {
		const html = render(<EvaluationBody evaluation={base} />);
		expect(html.indexOf('data-testid="evaluation-skipped"')).toBeGreaterThan(-1);
		expect(html.indexOf('data-testid="evaluation-skipped"')).toBeLessThan(html.indexOf('data-testid="evaluation-report-waiting"'));
	});

	it("shows Paused and Resume while a running evaluation is paused (A8.pause)", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		const html = render(<EvaluationBody evaluation={{ ...base, paused: true }} />, hostedSession("member"));
		expect(html).toMatch(
			/data-testid="evaluation-status"[^>]*data-status="paused"|data-status="paused"[^>]*data-testid="evaluation-status"/,
		);
		expect(html).toContain(">Paused<");
		expect(html).toContain('data-testid="evaluation-unpause"');
		expect(html).not.toContain('data-testid="evaluation-pause"');
	});

	it("shows the sum of the benchmarks' caps as the budget (D7.result)", () => {
		const html = render(
			<EvaluationBody
				evaluation={{
					...base,
					spec: { ...base.spec, budget_usd: 1 },
					children: [{ benchmark: "hd-harbor-tb2", experiment_id: "e1", status: "running", budget_usd: 1.1 }],
				}}
			/>,
		);
		// Q-135: the label says Budget once, the value is the amount alone.
		expect(html).toMatch(/data-testid="evaluation-budget">\$1\.10</);
	});
});
describe("EvaluationBody finished progress (N142)", () => {
	it("drops the live note once finished and names the runs that could not run", async () => {
		const { useGetExperimentQuery } = await import("@/lib/store/apis/benchApi");
		vi.mocked(useGetExperimentQuery).mockReturnValue(
			okQuery({ exp_id: "e1", slots: { total: 30, done: 29, running: 0, failed: 1 } }) as never,
		);
		const done: Evaluation = { ...base, status: "done", children: [{ benchmark: "hd-harbor-tb2", experiment_id: "e1", status: "done" }] };
		const html = render(<EvaluationBody evaluation={done} />);
		expect(html).not.toContain("Updates every 3 seconds");
		expect(html).toContain("One comparison per benchmark.");
		expect(html).toContain("29 of 30 runs scored, 1 could not run (infrastructure error)");
		const running = render(<EvaluationBody evaluation={base} />);
		expect(running).toContain("Updates every 3 seconds.");
		vi.mocked(useGetExperimentQuery).mockReturnValue(
			okQuery({ exp_id: "e1", slots: { total: 20, done: 7, running: 2, failed: 0 } }) as never,
		);
	});
});
describe("EvaluationBody why runs did not start (fix wave 3, H14)", () => {
	it("shows the child's run problem and the job log instead of Waiting to start (N180, N216)", async () => {
		const { useGetExperimentQuery } = await import("@/lib/store/apis/benchApi");
		vi.mocked(useGetExperimentQuery).mockReturnValue(
			okQuery({
				exp_id: "e1",
				slots: { total: 3, done: 0, running: 0, failed: 0 },
				problem: "worker shared could not take this run: Modal refused the sandbox",
				run_job_id: "job_9",
			}) as never,
		);
		const queued: Evaluation = { ...base, children: [{ benchmark: "hd-harbor-tb2", experiment_id: "e1", status: "queued" }] };
		const html = render(<EvaluationBody evaluation={queued} />);
		// A known cause reads as one sentence; the raw text sits in Technical details (M3-9).
		expect(html).toContain("The worker refused the runs of hd-harbor-tb2.");
		expect(html).toContain("worker shared could not take this run: Modal refused the sandbox");
		expect(html).toContain('href="/jobs/job_9"');
		expect(html).toContain("Open the job log");
		expect(html).not.toContain("Waiting to start");
		vi.mocked(useGetExperimentQuery).mockReturnValue(
			okQuery({ exp_id: "e1", slots: { total: 20, done: 7, running: 2, failed: 0 } }) as never,
		);
	});

	const skippedBy = (skipped: string): Evaluation => ({
		...base,
		status: "failed",
		children: [{ benchmark: "hd-harbor-bfcl", experiment_id: "", status: "skipped", skipped }],
	});
	const noScan = "plan experiment: skill pdf-tools in agents/zz-loop/skills has no passing scan: open the agent's Tools tab and press Scan";
	const changed =
		"plan experiment: skill pdf-tools in agents/zz-loop/skills changed after its scan: open the agent's Tools tab and press Scan";

	it("turns a skill gate refusal into a next step with Scan it now and the agent's tools (L5)", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		const html = render(<EvaluationBody evaluation={skippedBy(noScan)} />, hostedSession("member"));
		expect(html).toContain("Skill pdf-tools of agent zz-loop has not passed a safety scan, so runs of this agent are refused.");
		expect(html).toContain('data-testid="evaluation-skill-scan-hd-harbor-bfcl"');
		expect(html).toContain("Scan it now");
		expect(html).toContain('href="/agents/zz-loop?tab=tools"');
		const html2 = render(<EvaluationBody evaluation={skippedBy(changed)} />, hostedSession("member"));
		expect(html2).toContain("Skill pdf-tools changed after its last scan, so runs of this agent are refused.");
	});

	it("offers Scan it now for both skill gate sentences adapters/skillgate.go writes (fix wave 3 integration)", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		// The exact sentences pinned by adapters/skillgate_test.go, bare and as the planner wraps them.
		const pinned = [
			"skill review in agents/codex/skills has no passing scan: open the agent's Tools tab and press Scan",
			"skill review in agents/codex/skills changed after its scan: open the agent's Tools tab and press Scan",
		];
		for (const sentence of pinned) {
			for (const skipped of [sentence, `plan experiment: ${sentence}`]) {
				const html = render(<EvaluationBody evaluation={skippedBy(skipped)} />, hostedSession("member"));
				expect(html).toContain('data-testid="evaluation-skill-scan-hd-harbor-bfcl"');
				expect(html).toContain("Scan it now");
				expect(html).toContain('href="/agents/codex?tab=tools"');
			}
		}
	});

	it("shows the scan result: admitted, then blocked with its findings (L5)", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		const { useScanAgentSkillMutation } = await import("@/lib/store/apis/agentopsApi");
		const result = (decision: string, findings: object[] = []) =>
			[() => undefined, { isLoading: false, data: { installed: true, skill: {}, record: { decision, counts: {}, findings } } }] as never;
		vi.mocked(useScanAgentSkillMutation).mockReturnValue(result("admitted"));
		expect(render(<EvaluationBody evaluation={skippedBy(noScan)} />, hostedSession("member"))).toContain(
			"Passed the scan. Start the evaluation again to run this benchmark.",
		);
		vi.mocked(useScanAgentSkillMutation).mockReturnValue(
			result("blocked", [{ rule: "exfil", severity: "block", file: "run.sh", line: 3, detail: "sends files to a remote host" }]),
		);
		const blocked = render(<EvaluationBody evaluation={skippedBy(noScan)} />, hostedSession("member"));
		expect(blocked).toContain("sends files to a remote host");
		expect(blocked).toContain("Remove the skill on the Tools tab");
		vi.mocked(useScanAgentSkillMutation).mockReturnValue([() => undefined, { isLoading: false }] as never);
	});

	it("puts the rerun notice below the header, full width (N193)", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		const { useRerunEvaluationFailuresMutation } = await import("@/lib/store/apis/runControlApi");
		vi.mocked(useRerunEvaluationFailuresMutation).mockReturnValue([
			() => undefined,
			{ isLoading: false, data: { evaluation: { id: "ev_2" }, children: [{}, {}] } },
		] as never);
		const done = { ...base, status: "done", failed_runs: 2 } as Evaluation;
		const html = render(<EvaluationBody evaluation={done} />, hostedSession("member"));
		const notice = html.indexOf('data-testid="evaluation-rerun-started"');
		expect(notice).toBeGreaterThan(-1);
		const header = html.slice(html.indexOf('data-testid="page-header"'), html.indexOf("</header>"));
		expect(header).not.toContain("evaluation-rerun-started");
		expect(html).toContain("Started ev_2 with 2 benchmarks");
		vi.mocked(useRerunEvaluationFailuresMutation).mockReturnValue([() => undefined, { isLoading: false }] as never);
	});

	it("says which benchmark is starting while the launch runs, and the evaluation's problem (N182)", () => {
		const starting = { ...base, status: "queued", children: [], launch: { started: 1, of: 3, current: "hd-harbor-bfcl" } } as Evaluation;
		expect(render(<EvaluationBody evaluation={starting} />)).toContain("Starting benchmark 2 of 3: hd-harbor-bfcl...");
		const broken = {
			...base,
			status: "failed",
			children: [],
			problem: "The server restarted while this evaluation was starting. Start it again.",
		} as Evaluation;
		const html = render(<EvaluationBody evaluation={broken} />);
		expect(html).toMatch(/data-testid="evaluation-problem"[^>]*>[^<]*The server restarted while this evaluation was starting/);
		expect(html).not.toContain("Starting benchmark");
	});

	it("prints the budget stop with the next run's expected cost (N190)", () => {
		const stopped = {
			...base,
			status: "done",
			children: [
				{
					benchmark: "hd-harbor-tb2",
					experiment_id: "e1",
					status: "done",
					budget_usd: 0.01,
					spent_usd: 0.0094,
					budget_stopped: true,
					not_started: 4,
					next_run_usd: 0.003,
				},
			],
		} as unknown as Evaluation;
		const html = render(<EvaluationBody evaluation={stopped} />);
		expect(html).toContain(
			"Stopped by the budget: spent $0.0094 of $0.01; the next run was expected to cost about $0.0030. 4 runs did not start.",
		);
		// The raise field is under the report card headline (see evaluationReportCard.budget.test.tsx).
		expect(html).toContain('data-testid="report-card-mock" data-evaluation="ev_1"');
	});

	it("marks a scheduled repeat (N297)", () => {
		const html = render(<EvaluationBody evaluation={{ ...base, schedule: "nightly" }} />);
		expect(html).toContain('data-testid="evaluation-scheduled"');
		expect(html).toContain(">Scheduled<");
		expect(html).toContain("Repeat of Codex v1 vs v2 by schedule nightly, ");
	});
});