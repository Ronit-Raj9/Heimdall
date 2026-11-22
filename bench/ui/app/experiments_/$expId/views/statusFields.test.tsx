import "@/lib/i18n";

import { releasableExperiments } from "@/components/releases/releases.utils";
import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import type { ExperimentStatus } from "@/lib/types/bench";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AnalysisTab } from "./analysisTab";
import { ExperimentVerdictPromote } from "./verdictTab";

// The experiment status as GET /experiments/{id} sends it (handlers.BuildStatus): typed with the
// shared ExperimentStatus, no local casts, so a renamed or dropped field fails tsc here.
function status(over: Partial<ExperimentStatus>): ExperimentStatus {
	return {
		exp_id: "e_x",
		name: "codex v2 vs v1",
		state: "done",
		manifest_hash: "sha256:abc",
		frozen_at: "2026-11-23T10:00:00Z",
		slots: { total: 4, done: 4, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0.42 },
		suite: "hd-harbor-tb2",
		arms: ["A", "B"],
		setups: [
			{ arm: "A", agent: "codex", agent_file: "agents/codex.yaml", model: "gpt-5.4-mini" },
			{ arm: "B", agent: "codex", agent_file: "agents/codex.yaml", model: "gpt-6-luna" },
		],
		...over,
	};
}

const api = vi.hoisted(() => ({ experiment: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetVerdictQuery: () => okQuery({ verdict: { decision: "inconclusive", action: "no_decision" } }),
	useGetClustersQuery: () => okQuery({ clusters: [] }),
	useGetExperimentQuery: () => api.experiment,
	useGetAgentsQuery: () => okQuery({ agents: [] }),
}));
vi.mock("@/lib/store/apis/integrityApi", () => ({
	useGetAwarenessQuery: () => okQuery({ label: "Heuristic phrase scan.", per_arm: {}, not_scanned: [] }),
}));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetJobQuery: () => ({}),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/apis/analysisApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/analysisApi")>()),
	useAuditTableMutation: () => mutation(),
}));

beforeEach(() => {
	api.experiment = okQuery(status({}));
});

describe("Compute blame reads multi_agent from the experiment status", () => {
	it("is enabled on a multi-agent run and disabled on a single-agent one", () => {
		api.experiment = okQuery(status({ multi_agent: true }));
		expect(tagOf(render(<AnalysisTab expId="e_x" />), "job-run-blame.run")).not.toMatch(/\sdisabled=""/);
		api.experiment = okQuery(status({}));
		expect(tagOf(render(<AnalysisTab expId="e_x" />), "job-run-blame.run")).toMatch(/\sdisabled=""/);
	});
});

describe("Promote reads arms_differ and setups from the experiment status", () => {
	it("offers setup B's model on a model-only comparison", () => {
		api.experiment = okQuery(status({ arms_differ: "model" }));
		const html = render(<ExperimentVerdictPromote expId="e_x" arms={["A", "B"]} decision="inconclusive" />);
		expect(html).not.toContain('data-testid="verdict-promote"');
		expect(html).toContain("Use model gpt-6-luna");
	});
	it("promotes on an agent change", () => {
		api.experiment = okQuery(status({ arms_differ: "agent" }));
		const html = render(<ExperimentVerdictPromote expId="e_x" arms={["A", "B"]} decision="improved" />);
		expect(html).toContain('data-testid="verdict-promote"');
	});
});

describe("The release list reads arms_differ and debug from the experiment status", () => {
	it("keeps only agent comparisons that are not debug copies", () => {
		const list = [
			status({ exp_id: "e_agent", arms_differ: "agent" }),
			status({ exp_id: "e_model", arms_differ: "model" }),
			status({ exp_id: "e_aa", arms_differ: "none" }),
			status({ exp_id: "e_dbg", arms_differ: "agent", debug: "replay", debug_from_run: "r_1" }),
			status({ exp_id: "e_old" }),
		];
		expect(releasableExperiments(list).map((e) => e.exp_id)).toEqual(["e_agent", "e_old"]);
	});
});
