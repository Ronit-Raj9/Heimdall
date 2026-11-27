import "@/lib/i18n";

import type { ExperimentStatus } from "@/lib/types/bench";
import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { certifyCandidates, MonitorCertifyForm, monitorFiles } from "./monitorCertifyForm";

// Fix wave 3, N255: Test a monitor picks a monitor settings file and finished comparisons from
// lists (no typed paths or ids) and no longer says it works on a local server only.

const exp = (exp_id: string, name: string, state: ExperimentStatus["state"], extra: Partial<ExperimentStatus> = {}): ExperimentStatus =>
	({ exp_id, name, state, frozen_at: "2026-11-20T10:00:00Z", finished_at: "2026-11-20T11:00:00Z", ...extra }) as ExperimentStatus;

const EXPS = [
	exp("e_good", "codex vs claude", "done"),
	exp("e_running", "still going", "running"),
	exp("e_debug", "debug of run r_1", "done", { debug: "r_1" }),
];

vi.mock("@/lib/store/apis/projectFilesApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/projectFilesApi")>()),
	useListProjectFilesQuery: () =>
		okQuery({
			area: "policies",
			dir: "",
			entries: [
				{ path: "default.yaml", size: 1, dir: false, mod_time: "" },
				{ path: "monitors", size: 0, dir: true, mod_time: "" },
				{ path: "monitors/guard.yaml", size: 1, dir: false, mod_time: "" },
				{ path: "monitors/notes.txt", size: 1, dir: false, mod_time: "" },
			],
		}),
}));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useListExperimentsQuery: () => okQuery({ experiments: EXPS }),
}));
vi.mock("@/components/jobs/jobRunButton", () => ({ JobRunButton: () => <button data-testid="job-run" /> }));

describe("MonitorCertifyForm", () => {
	it("offers pickers, never text boxes for paths or ids, and no local-only note", () => {
		const html = render(<MonitorCertifyForm />);
		expect(html).toContain('data-testid="monitor-certify-file"');
		expect(html).toContain(">policies/monitors/guard.yaml<");
		expect(html).not.toContain("default.yaml");
		expect(html).toContain('data-testid="monitor-certify-exp-e_good"');
		expect(html).not.toContain("monitor-certify-exp-e_running");
		expect(html).not.toContain("monitor-certify-exp-e_debug");
		expect(html).not.toContain('data-testid="monitor-certify-path"');
		expect(html).not.toContain("local server only");
		expect(html).toContain("No model calls.");
	});

	it("lists monitor files and finished, searchable comparisons", () => {
		expect(monitorFiles(undefined)).toEqual([]);
		expect(certifyCandidates(EXPS, "").map((e) => e.exp_id)).toEqual(["e_good"]);
		expect(certifyCandidates(EXPS, "CLAUDE").map((e) => e.exp_id)).toEqual(["e_good"]);
		expect(certifyCandidates(EXPS, "nothing")).toEqual([]);
	});
});