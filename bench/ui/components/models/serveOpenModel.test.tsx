import "@/lib/i18n";

import { errorQuery, okQuery, render } from "@/lib/testing/render";
import { LOCAL_SESSION, type SessionInfo } from "@/lib/session/sessionContext";
import type { OpenModelCatalog, OpenModelDeployment } from "@/lib/store/apis/openModelsApi";
import { activeDeployments, defaultGPU, maxCost } from "@/lib/store/apis/openModelsApi";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ServeOpenModel } from "./serveOpenModel";

const api = vi.hoisted(() => ({ q: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/openModelsApi", async (orig) => {
	const real = await orig<typeof import("@/lib/store/apis/openModelsApi")>();
	const { mutation } = await import("@/lib/testing/render");
	return {
		...real,
		useGetOpenModelCatalogQuery: () => api.q.catalog,
		useListOpenModelsQuery: () => api.q.list,
		useStartOpenModelMutation: () => mutation(),
		useStopOpenModelMutation: () => mutation(),
	};
});

// M8-15: the evaluation is picked from the experiments list.
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/benchApi")>()),
	useListExperimentsQuery: () => ({
		data: { experiments: [{ exp_id: "e_open", name: "Support run", state: "running" }, { exp_id: "e_done", name: "Old", state: "done" }] },
	}),
}));

const CATALOG: OpenModelCatalog = {
	engines: [
		{ id: "vllm", title: "vLLM", version: "0.33.0", provider: "vllm", licence: "Apache-2.0" },
		{ id: "sglang", title: "SGLang", version: "0.5.23", provider: "sgl", licence: "Apache-2.0" },
	],
	models: [
		{
			id: "Qwen/Qwen2.5-0.5B-Instruct",
			title: "Qwen2.5 0.5B Instruct",
			family: "Qwen",
			params_b: 0.5,
			licence: "Apache-2.0",
			min_vram_mb: 6000,
			max_model_len: 8192,
			tool_calls: true,
			gpus: [
				{ name: "T4", title: "NVIDIA T4 16 GB", memory_mb: 15360, usd_per_hour: 0.59, count: 1, usd_per_hour_total: 0.59 },
				{ name: "L4", title: "NVIDIA L4 24 GB", memory_mb: 23034, usd_per_hour: 0.8, count: 1, usd_per_hour_total: 0.8, recommended: true },
			],
		},
	],
};

const READY: OpenModelDeployment = {
	id: "abcdefghij",
	model: "Qwen/Qwen2.5-0.5B-Instruct",
	title: "Qwen2.5 0.5B Instruct",
	engine: "vllm",
	provider: "vllm",
	model_name: "vllm/Qwen/Qwen2.5-0.5B-Instruct",
	gpu: "L4",
	gpu_count: 1,
	usd_per_hour: 0.8,
	max_usd: 1.6,
	app_name: "heimdall-om-abcdefghij",
	state: "ready",
	experiment_id: "exp-1",
	created_by: "usr_1",
	created_at: "2026-11-22T10:00:00Z",
	expires_at: "2026-11-22T12:00:00Z",
};

const hosted = (role: "admin" | "viewer"): SessionInfo => ({
	...LOCAL_SESSION,
	authMode: "hosted",
	kind: "user",
	signedIn: true,
	status: "ready",
	role,
	org: { id: "org_a", name: "A", slug: "a", role },
} as SessionInfo);

describe("ServeOpenModel", () => {
	beforeEach(() => {
		api.q = { catalog: okQuery(CATALOG), list: okQuery({ deployments: [READY] }) };
	});
	it("is hidden in local mode", () => {
		expect(render(<ServeOpenModel />)).toBe("");
	});
	it("lets an admin pick a model, GPU and time limit, and shows the cost bound", () => {
		const html = render(<ServeOpenModel />, hosted("admin"));
		expect(html).toContain('data-testid="serve-form"');
		expect(html).toContain("cheapest fit");
		expect(html).toContain("At most $1.60");
		expect(html).toContain("vllm/Qwen/Qwen2.5-0.5B-Instruct");
		expect(html).toContain('data-testid="serve-stop-abcdefghij"');
		expect(html).toContain("For evaluation exp-1");
		// Start is the shared paid button; the evaluation is a select of unfinished ones.
		expect(html).toContain('data-testid="serve-start"');
		expect(html).toContain(">Support run<");
		expect(html).not.toContain(">Old<");
	});
	it("shows a viewer the deployments only", () => {
		const html = render(<ServeOpenModel />, hosted("viewer"));
		expect(html).not.toContain('data-testid="serve-form"');
		expect(html).not.toContain("serve-stop-");
		expect(html).toContain('data-testid="serve-state-abcdefghij"');
	});
	it("says how to start when nothing runs", () => {
		api.q.list = okQuery({ deployments: [] });
		expect(render(<ServeOpenModel />, hosted("viewer"))).toContain("Connect Modal under Workers first");
	});
	it("renders when the list fails", () => {
		api.q.list = errorQuery(503);
		expect(render(<ServeOpenModel />, hosted("admin"))).toContain('data-testid="serve-empty"');
	});
});

describe("open model helpers", () => {
	it("picks the recommended GPU and bounds the cost", () => {
		expect(defaultGPU(CATALOG.models[0])).toBe("L4");
		expect(maxCost(CATALOG.models[0].gpus[1], 2)).toBe(1.6);
		expect(maxCost(undefined, 2)).toBe(0);
		expect(activeDeployments([READY, { ...READY, id: "x", state: "stopped" }]).map((d) => d.id)).toEqual(["abcdefghij"]);
	});
});

