import type fw13En from "@/locales/en/fw13.json";

// Types the "fw13" namespace (locales/en/fw13.json): the agent page's delete action.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw13: typeof fw13En;
	}
}

export {};