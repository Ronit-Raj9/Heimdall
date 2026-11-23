import type hw17En from "@/locales/en/hw17.json";

// Types the "hw17" namespace (locales/en/hw17.json), fix wave 3 lane H17: the Actions tab's own failure tests.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw17: typeof hw17En;
	}
}

export {};