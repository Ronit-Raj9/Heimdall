import type reportCardEn from "@/locales/en/reportCard.json";
import type uf3En from "@/locales/en/uf3.json";

// Types the "reportCard" namespace (locales/en/reportCard.json) and the lane 3 fix wave namespace (locales/en/uf3.json).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		reportCard: typeof reportCardEn;
		uf3: typeof uf3En;
	}
}

export {};
