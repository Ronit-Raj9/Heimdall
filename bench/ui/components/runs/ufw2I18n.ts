import type ufw2En from "@/locales/en/ufw2.json";

// Types the "ufw2" namespace (locales/en/ufw2.json): pass W lane W2, agents, runs, failures and improvements.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		ufw2: typeof ufw2En;
	}
}

export {};
