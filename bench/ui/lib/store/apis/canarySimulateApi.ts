import { benchApi } from "./benchApi";

/** POST /canaries/simulate (handlers/canarysimulate.go CanarySimulateRequest). */
export interface CanarySimulateBody {
	tasks: number;
	trials?: number;
	sensitivity?: string;
	drop?: number;
	latency_rise?: number;
}

/** The parts of canary.SimResult the page shows. */
export interface CanarySimResult {
	alpha: number;
	triggers: number;
	alarm_per_trigger: number;
	any_alarm_30: number;
	any_alarm_365: number;
	detect_first_trigger: number;
	detect_by_3: number;
}

export function canarySimulateRequest(body: CanarySimulateBody) {
	return { url: "/canaries/simulate", method: "POST", body } as const;
}

/** Round 5 lane L8: "What if" on a drift monitor. */
export const canarySimulateApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		simulateCanary: builder.mutation<CanarySimResult, CanarySimulateBody>({ query: canarySimulateRequest }),
	}),
});

export const { useSimulateCanaryMutation } = canarySimulateApi;