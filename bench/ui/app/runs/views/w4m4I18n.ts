import type w4m4En from "@/locales/en/w4m4.json";

// Types the "w4m4" namespace (locales/en/w4m4.json): runs, run page and MCP test minors.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w4m4: typeof w4m4En;
	}
}

export {};