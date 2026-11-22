// Types the "mcptest" namespace (lane Z07, /mcp-test) against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		mcptest: typeof import("@/locales/en/mcptest.json");
	}
}

export {};