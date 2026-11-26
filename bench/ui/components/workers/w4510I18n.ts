import type w4510En from "@/locales/en/w4510.json";

// Types the "w4510" namespace (locales/en/w4510.json): the line under the evaluate step's worker
// picker when a sandbox platform takes one evaluation (N208).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4510: typeof w4510En;
	}
}

export {};