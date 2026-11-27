import type uf2aEn from "@/locales/en/uf2a.json";

// Types the "uf2a" namespace (locales/en/uf2a.json): new texts of the Home, Quickstart, Agents
// and jobs fixes whose catalogs belong to other lanes.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf2a: typeof uf2aEn;
	}
}

export {};
