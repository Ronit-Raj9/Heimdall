import type hw15En from "@/locales/en/hw15.json";

// Types the "hw15" namespace (locales/en/hw15.json): model pickers on the authoring pages (fix wave 3, lane H15).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw15: typeof hw15En;
	}
}

export {};