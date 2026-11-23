import type hw03En from "@/locales/en/hw03.json";

// Types the "hw03" namespace (locales/en/hw03.json): fix wave 3 lane H03 strings that live in no
// namespace the lane owns (the invitation revoke confirmation of the Members tab).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		hw03: typeof hw03En;
	}
}

export {};