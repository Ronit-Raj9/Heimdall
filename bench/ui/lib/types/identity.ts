// Identity (Wave A): email verification and the server admin's signup mode (auth/verify.go,
// auth/signupmode.go, handlers/identity.go).
import type { EmailDelivery, SignupPolicy } from "@/lib/types/bench";

/** POST /auth/verify-email: the token from the link. */
export interface VerifyEmailRequest {
	token: string;
}

/** POST /auth/verify-email answer. */
export interface VerifyEmailResult {
	email: string;
	verified: boolean;
}

/** POST /auth/verify-email/resend answer. The link itself is never in it. */
export interface VerifySent {
	email: string;
	delivery: EmailDelivery;
	already_verified?: boolean;
	expires_at?: string;
}

/** GET /admin/signup: the effective signup mode and where it comes from. */
export interface SignupSettings {
	mode: SignupPolicy;
	domains: string[];
	/** config: deploy/bench.yaml or the environment; admin: changed on this page. */
	source: "config" | "admin";
	config_mode: SignupPolicy;
	config_domains: string[];
	updated_by?: string;
	updated_at?: string;
	email_delivery: EmailDelivery;
}

/** POST /admin/signup. An empty mode goes back to the server config. */
export interface SignupSettingsRequest {
	mode: SignupPolicy | "";
	domains: string[];
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		identity: typeof import("@/locales/en/identity.json");
	}
}
