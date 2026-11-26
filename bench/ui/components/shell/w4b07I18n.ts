import type w4b07En from "@/locales/en/w4b07.json";

// Types the "w4b07" namespace (locales/en/w4b07.json): wave 4 lane I11 strings (who sees what).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4b07: typeof w4b07En;
	}
}

export {};