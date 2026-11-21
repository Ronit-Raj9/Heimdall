import type { ModelRef } from "@/components/models/modelPicker";

/** GET /docs: one in-app guide. */
export interface DocSummary {
	name: string;
	title: string;
}

export interface DocList {
	docs: DocSummary[];
}

/** GET /docs/{name}. */
export interface DocBody {
	name: string;
	title: string;
	markdown: string;
}

/** POST /quickrun (lane L28 contract): one call gives a running experiment. */
export interface QuickRunRequest {
	suite: string;
	agent: string;
	model: ModelRef;
	tasks?: number;
	trials?: number;
}

export interface QuickRunResponse {
	exp_id: string;
	estimate?: unknown;
}
