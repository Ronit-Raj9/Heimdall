import type { ModelRef } from "@/components/models/modelPicker";
import type { ExperimentPlan, ExperimentStatus, Forecast } from "@/lib/types/bench";

/** Body of POST /quickrun (handlers/quickrun.go QuickRunRequest). */
export interface QuickRunRequest {
	suite?: string;
	agent?: string;
	model: ModelRef;
	/** Arm B; without it arm B repeats arm A (an A/A run). */
	compare_model?: ModelRef;
	tasks?: number;
	trials?: number;
	budget_usd?: number;
	/** false stops after create. */
	start?: boolean;
}

/** Answer of POST /quickrun. */
export interface QuickRunResponse {
	mode?: string;
	exp_id: string;
	path: string;
	manifest_hash: string;
	started: boolean;
	plan: ExperimentPlan;
	estimate: Forecast | null;
	status?: ExperimentStatus;
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		quickrun: typeof import("@/locales/en/quickrun.json");
	}
}
