import type ufrjEn from "@/locales/en/ufrj.json";

// Types the "ufrj" namespace (locales/en/ufrj.json): round 2 lane RJ strings of Checks, the gateway, failure tests and the MCP test.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufrj: typeof ufrjEn;
	}
}

export {};
