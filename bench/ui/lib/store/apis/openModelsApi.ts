// Open models on the org's own Modal account (lane Z05, handlers/openmodels.go): pick a model, an
// engine and a GPU; Heimdall serves it with scale to zero and registers it as the org's vllm or sgl
// provider key while it runs, then stops it when its evaluation ends or its time limit passes.
import { benchApi } from "./benchApi";

export type OpenModelState = "starting" | "ready" | "stopping" | "stopped" | "failed";

export interface OpenModelGPU {
	name: string;
	title: string;
	memory_mb: number;
	usd_per_hour: number;
	count: number;
	usd_per_hour_total: number;
	recommended?: boolean;
	unsupported?: string;
}

export interface OpenModelEntry {
	id: string;
	title: string;
	family: string;
	params_b: number;
	licence: string;
	min_vram_mb: number;
	max_model_len: number;
	tool_calls: boolean;
	reasoning?: boolean;
	gated?: boolean;
	gpus: OpenModelGPU[];
}

export interface OpenModelEngine {
	id: string;
	title: string;
	version: string;
	provider: string;
	licence: string;
}

export interface OpenModelCatalog {
	models: OpenModelEntry[];
	engines: OpenModelEngine[];
}

export interface OpenModelDeployment {
	id: string;
	model: string;
	title: string;
	engine: string;
	provider: string;
	model_name: string;
	gpu: string;
	gpu_count: number;
	usd_per_hour: number;
	max_usd: number;
	app_name: string;
	base_url?: string;
	key_last4?: string;
	experiment_id?: string;
	state: OpenModelState | string;
	detail?: string;
	created_by: string;
	created_at: string;
	ready_at?: string;
	expires_at: string;
	stopped_at?: string;
	stop_reason?: string;
}

export interface StartOpenModelRequest {
	model: string;
	engine?: string;
	gpu?: string;
	experiment_id?: string;
	max_hours?: number;
}

export const openModelsApi = benchApi.enhanceEndpoints({ addTagTypes: ["OpenModels", "BenchModels"] }).injectEndpoints({
	endpoints: (builder) => ({
		getOpenModelCatalog: builder.query<OpenModelCatalog, void>({
			query: () => "/open-models/catalog",
		}),
		listOpenModels: builder.query<{ deployments: OpenModelDeployment[] }, void>({
			query: () => "/open-models",
			providesTags: ["OpenModels"],
		}),
		startOpenModel: builder.mutation<OpenModelDeployment, StartOpenModelRequest>({
			query: (body) => ({ url: "/open-models", method: "POST", body }),
			invalidatesTags: ["OpenModels", "AuditLog"],
		}),
		stopOpenModel: builder.mutation<OpenModelDeployment, string>({
			query: (id) => ({ url: `/open-models/${encodeURIComponent(id)}/stop`, method: "POST" }),
			invalidatesTags: ["OpenModels", { type: "BenchModels", id: "LIST" }, "AuditLog"],
		}),
	}),
});

export const { useGetOpenModelCatalogQuery, useListOpenModelsQuery, useStartOpenModelMutation, useStopOpenModelMutation } = openModelsApi;

/** Deployments still on Modal (starting, ready or being stopped). */
export function activeDeployments(list: OpenModelDeployment[]): OpenModelDeployment[] {
	return list.filter((d) => d.state === "starting" || d.state === "ready" || d.state === "stopping");
}

/** The GPU the form picks first: the recommended one, else the first that fits. */
export function defaultGPU(m: OpenModelEntry | undefined): string {
	if (!m) return "";
	return (m.gpus.find((g) => g.recommended) ?? m.gpus.find((g) => g.count > 0))?.name ?? "";
}

/** The most the GPUs can cost over the time limit, in USD, rounded to cents. */
export function maxCost(g: OpenModelGPU | undefined, hours: number): number {
	if (!g || !(hours > 0)) return 0;
	return Math.round(g.usd_per_hour_total * hours * 100) / 100;
}
