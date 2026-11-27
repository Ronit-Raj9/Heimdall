import type ufc4En from "@/locales/en/ufc4.json";

// Types the "ufc4" namespace (locales/en/ufc4.json): UI fix wave phase C lane C4 strings of the
// model picker, the run page and the MCP test.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufc4: typeof ufc4En;
	}
}

export {};
