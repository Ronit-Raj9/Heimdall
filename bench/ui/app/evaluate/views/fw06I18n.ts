import type fw06En from "@/locales/en/fw06.json";

// Types the "fw06" namespace (locales/en/fw06.json): wizard step 3 texts about the other setups.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		fw06: typeof fw06En;
	}
}

export {};