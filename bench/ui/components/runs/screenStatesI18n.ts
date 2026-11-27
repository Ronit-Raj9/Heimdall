import type ufs2En from "@/locales/en/ufs2.json";

// Types the "ufs2" namespace (locales/en/ufs2.json): shared error, loading and list texts of lane S2.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufs2: typeof ufs2En;
	}
}

export {};
