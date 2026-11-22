/** GET /integrations/bifrost and GET /evaluations/{id}/bifrost (handlers/bifrostuse.go). */
export type BifrostFeatureState = "on" | "available" | "off" | "unknown";

export interface BifrostFeature {
	id: string;
	name: string;
	state: BifrostFeatureState;
	detail: string;
	docs: string;
	code?: string;
}

export interface BifrostUse {
	gateway_configured: boolean;
	gateway_host?: string;
	reachable: boolean;
	check_error?: string;
	loaded_plugins: string[];
	features: BifrostFeature[];
}

export interface EvaluationBifrostUse extends BifrostUse {
	evaluation_id: string;
	uses: BifrostFeature[];
}
