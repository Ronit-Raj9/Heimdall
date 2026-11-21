// Govern area (web parity lane L16): release policy editor types, mirroring handlers/policies.go.

/** schemas.PolicyCheck: one rule. Exactly one of rule, max, min or equals applies. */
export interface PolicyCheck {
	id: string;
	description?: string;
	metric: string;
	condition?: string;
	arm?: string;
	rule?: "no_regression" | "improved" | "";
	max?: number;
	min?: number;
	equals?: number;
	min_n?: number;
	severity?: "block" | "warn" | "";
}

/** schemas.Policy: a release policy document. */
export interface ReleasePolicy {
	policy_version: number;
	name: string;
	description?: string;
	on_unknown?: "block" | "warn" | "";
	checks: PolicyCheck[];
	judge_calibration?: Record<string, number>;
}

export interface PolicySummary {
	file: string;
	path: string;
	name?: string;
	description?: string;
	checks: number;
	valid: boolean;
	error?: string;
	sha256: string;
	size: number;
}

export interface PolicyList {
	policies: PolicySummary[];
}

export interface PolicyDocument {
	file: string;
	path: string;
	yaml: string;
	sha256: string;
	policy?: ReleasePolicy;
	valid: boolean;
	error?: string;
}

/** Body of PUT /policies/{name} and POST /policies/validate: yaml or policy, never both. */
export interface PolicySaveRequest {
	yaml?: string;
	policy?: ReleasePolicy;
	if_match?: string;
	note?: string;
}

export interface PolicyValidation {
	valid: boolean;
	error?: string;
	yaml: string;
	policy?: ReleasePolicy;
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		govern: typeof import("@/locales/en/govern.json");
	}
}
