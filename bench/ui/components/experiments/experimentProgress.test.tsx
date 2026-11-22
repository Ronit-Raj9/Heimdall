import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { ExperimentStatus } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { ExperimentProgressCard } from "./experimentProgress";

vi.mock("@/lib/hooks/useLiveExperiment", () => ({
	useLiveExperiment: () => ({ slots: [], events: [], live: true }),
}));
vi.mock("@/components/platform/experimentActions", () => ({ ExperimentRunActions: () => null }));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, params, children, ...rest }: { to: string; params?: Record<string, string>; children: unknown }) => {
		const href = Object.entries(params ?? {}).reduce((h, [k, v]) => h.replace(`$${k}`, v), to);
		return (
			<a href={href} {...rest}>
				{children as never}
			</a>
		);
	},
}));

const base: ExperimentStatus = {
	exp_id: "e_admit_zz_test_mcp_a0w1",
	name: "admit-zz-test-mcp",
	state: "created",
	manifest_hash: "sha256:x",
	frozen_at: "2026-11-23T10:00:00Z",
	slots: { total: 3, done: 0, running: 0, failed: 0 },
	spent_usd: { state: "observed", v: 0, evidence: "gateway" },
} as ExperimentStatus;

describe("ExperimentProgressCard run problem (N151)", () => {
	it("says why runs could not start and links the job log, in place of the waiting line", () => {
		const status = {
			...base,
			state: "failed",
			problem: "every worker refused this run 3 times: The worker's copy of your project does not have this benchmark yet.",
			run_job_id: "job_01abc",
		} as ExperimentStatus;
		const html = render(<ExperimentProgressCard status={status} />);
		expect(html).toContain('data-testid="experiment-progress-problem"');
		expect(html).toContain("Runs could not start: every worker refused this run 3 times: The worker&#x27;s copy of your project");
		expect(html).toContain('href="/jobs/job_01abc"');
		expect(html).toContain("Open the job log");
		expect(html).not.toContain("Waiting for the first run to start.");
	});

	it("keeps the waiting line when there is no problem", () => {
		const html = render(<ExperimentProgressCard status={base} />);
		expect(html).not.toContain('data-testid="experiment-progress-problem"');
		expect(html).toContain("Waiting for the first run to start.");
	});
});