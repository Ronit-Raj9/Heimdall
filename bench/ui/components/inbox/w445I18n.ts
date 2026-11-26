import type w445En from "@/locales/en/w445.json";

// Types the "w445" namespace (locales/en/w445.json), owned by the session import and the model draft of the inbox.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		w445: typeof w445En;
	}
}

export {};