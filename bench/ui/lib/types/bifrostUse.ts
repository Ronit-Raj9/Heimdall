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

/** One arm of the cache on vs off report (bifrostfeatures.CacheArmStats). */
export interface CacheArmStats {
	arm: string;
	runs: number;
	requests: number;
	cache_hits: number;
	hit_rate: number;
	mean_latency_ms: number;
	cost_usd: number;
	cost_runs: number;
	successes: number;
	success_runs: number;
	success_rate: number;
}

/** GET /evaluations/{id}/bifrost/cache (handlers/evalcacheab.go). */
export interface EvaluationCacheAB {
	evaluation_id: string;
	enabled: boolean;
	children: {
		benchmark: string;
		experiment_id: string;
		report: {
			off: CacheArmStats;
			on: CacheArmStats;
			tasks: number;
			latency_delta_ms: number;
			cost_saved_usd: number;
			success_delta: number;
			summary: string;
		};
	}[];
}
