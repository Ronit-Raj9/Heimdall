import "@/lib/i18n";

import type { ExperimentStatus } from "@/lib/types/bench";
import { okQuery, render } from "@/lib/testing/render";
import { Code2, FlaskConical, SquareTerminal } from "lucide-react";
import { describe, expect, it, vi } from "vitest";
import { ExperimentBoard, humanExperimentName, isAutoName, suiteIcon, verdictTone } from "./experimentBoard";

const rows: ExperimentStatus[] = [
	{
		exp_id: "e_quick1",
		name: "quick-20261121-101500-ab12",
		state: "running",
		manifest_hash: "h",
		frozen_at: "2026-11-21T10:15:00Z",
		started_at: "2026-11-21T10:15:00Z",
		slots: { total: 8, done: 3, running: 2, failed: 0 },
		spent_usd: { state: "observed", v: 0.42 },
		suite: "coding-core@1.0.0",
		arms: ["A", "B"],
	},
	{
		exp_id: "e_named",
		name: "Prompt v2 vs v1",
		state: "done",
		manifest_hash: "h",
		frozen_at: "2026-11-20T10:15:00Z",
		slots: { total: 4, done: 4, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 1.2 },
		suite: "terminal-ops@2.0.0",
		arms: ["A", "B"],
		decision: "improved",
	},
	{
		exp_id: "e_admit_acme_safety_5e8q",
		name: "admit-acme-safety",
		state: "done",
		manifest_hash: "h",
		frozen_at: "2026-11-19T10:15:00Z",
		slots: { total: 2, done: 2, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
		suite: "acme-safety@1.0.0",
		arms: ["oracle", "noop"],
	},
];

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("nuqs", () => ({
	parseAsString: {},
	useQueryStates: () => [{ q: null, state: null, suite: null }, () => undefined],
}));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useListExperimentsQuery: () => okQuery({ experiments: rows }),
}));

const t = (k: string, o?: Record<string, string>) => `${k}:${o?.arms ?? ""}:${o?.suite ?? ""}`;

describe("experiment list helpers", () => {
	it("builds a readable name for generated names only", () => {
		expect(isAutoName(rows[0])).toBe(true);
		expect(isAutoName(rows[1])).toBe(false);
		expect(humanExperimentName(rows[0], t)).toBe("quickRun::coding-core");
		expect(humanExperimentName({ ...rows[0], arms: ["codex-gpt", "claude-code"] }, t)).toBe("armsVs:codex-gpt vs claude-code:coding-core");
		expect(humanExperimentName(rows[1], t)).toBe("Prompt v2 vs v1");
		expect(humanExperimentName(rows[2], (k, o) => `${k}:${o?.name}`)).toBe("system.readiness:acme-safety");
	});
	it("maps verdicts to plain words and suites to icons", () => {
		expect(verdictTone("improved")).toEqual({ key: "improved", tone: "green" });
		expect(verdictTone("regressed").key).toBe("regressed");
		expect(verdictTone(undefined).key).toBe("pending");
		expect(suiteIcon("coding-core@1.0.0")).toBe(Code2);
		expect(suiteIcon("terminal-ops")).toBe(SquareTerminal);
		expect(suiteIcon("zzz")).toBe(FlaskConical);
	});
});

describe("ExperimentBoard", () => {
	it("renders readable names, status, verdict words, progress and the filter toolbar", () => {
		const html = render(<ExperimentBoard />);
		expect(html).toContain("Quick run on coding-core");
		expect(html).toContain("Prompt v2 vs v1");
		expect(html).toContain("B is better");
		expect(html).toContain("3 of 8");
		expect(html).toContain('data-testid="experiment-filter-status"');
		expect(html).toContain('<option value="running">Running</option>');
		expect(html).toContain('data-testid="experiment-filter-suite"');
		expect(html).toContain(">terminal-ops</option>");
		expect(html).not.toContain("experiment-chip-");
		expect(html).toContain("animate-ping");
		expect(html).not.toContain(">quick-20261121-101500-ab12<");
	});
	it("hides automatic readiness checks behind a toggle", () => {
		const html = render(<ExperimentBoard />);
		expect(html).not.toContain("experiment-row-e_admit_acme_safety_5e8q");
		expect(html).toContain("Show system checks (1)");
	});
});