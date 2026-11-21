import type experimentsUxEn from "@/locales/en/experimentsUx.json";

// Types the "experimentsUx" namespace (locales/en/experimentsUx.json), owned by the experiments list.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		experimentsUx: typeof experimentsUxEn;
	}
}

export {};