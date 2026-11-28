import type ufk5En from "@/locales/en/ufk5.json";

// Types the "ufk5" namespace (locales/en/ufk5.json): round 6 lane K5, caps on resume, retry, fork and fault test.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufk5: typeof ufk5En;
	}
}

export {};