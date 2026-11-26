import type w449En from "@/locales/en/w449.json";

// Types the "w449" namespace (locales/en/w449.json), owned by the validity ledger (flip banner and /validity).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w449: typeof w449En;
	}
}

export {};