// Account safety (L22): owner reset links and the reset page (auth/reset.go, handlers/accountsafety.go).

/** POST /orgs/{org}/members/{user}/reset-link: the token and link appear here once. */
export interface ResetLink {
	token: string;
	url: string;
	user_id: string;
	email: string;
	expires_at: string;
}

/** GET /auth/reset/{token}: the account the link resets. */
export interface ResetInfo {
	email: string;
	expires_at: string;
}

/** POST /auth/reset/{token}. */
export interface ResetRequest {
	password: string;
}

/** The captcha fields GET /auth/config adds when signup needs an hCaptcha response. */
export interface CaptchaConfig {
	captcha?: boolean;
	captcha_site_key?: string;
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		accountSafety: typeof import("@/locales/en/accountSafety.json");
	}
}
