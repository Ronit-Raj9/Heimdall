import type ufs1En from "@/locales/en/ufs1.json";

// Types the "ufs1" namespace (locales/en/ufs1.json): texts of the shared look lane (S1).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufs1: typeof ufs1En;
	}
}

export {};
