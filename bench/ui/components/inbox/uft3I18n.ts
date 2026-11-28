import type uft3En from "@/locales/en/uft3.json";

// Types the "uft3" namespace (locales/en/uft3.json): round 7 lane T3, Build a task from a failure.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uft3: typeof uft3En;
	}
}

export {};