import type uf2cEn from "@/locales/en/uf2c.json";

// Types the "uf2c" namespace (locales/en/uf2c.json): texts of the agent page fix wave lane 2c.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		uf2c: typeof uf2cEn;
	}
}

export {};
