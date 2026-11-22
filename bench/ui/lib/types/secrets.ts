/** An org secret's metadata (handlers/secrets.go Secret). The value is never sent back. */
export interface OrgSecret {
	id: string;
	name: string;
	created_by?: string;
	/** How created_by reads (a member's display name). */
	created_by_name?: string;
	created_at: string;
	updated_at: string;
}

export interface OrgSecretList {
	secrets: OrgSecret[];
}

export interface OrgSecretCreate {
	name: string;
	value: string;
}

export interface OrgSecretDeleted {
	id: string;
	deleted: boolean;
}

/** Same rule as toolproxy.CheckOrgSecretName: env-var style, max 64 chars, no server-reserved names. */
export const SECRET_NAME_PATTERN = /^[A-Z][A-Z0-9_]{0,63}$/;
export const RESERVED_SECRET_PREFIXES = ["HEIMDALL_", "BENCH_", "BIFROST_", "AWS_", "S3_"] as const;
export const RESERVED_SECRET_NAMES = ["PATH", "HOME", "USER", "SHELL", "PWD", "TMPDIR", "LD_PRELOAD", "LD_LIBRARY_PATH"] as const;
export const MAX_SECRET_BYTES = 16 * 1024;

export type SecretNameProblem = "required" | "format" | "reserved" | null;

/** Why a name cannot be a secret name, or null. */
export function secretNameProblem(name: string): SecretNameProblem {
	if (!name) return "required";
	if (!SECRET_NAME_PATTERN.test(name)) return "format";
	if ((RESERVED_SECRET_NAMES as readonly string[]).includes(name)) return "reserved";
	if (RESERVED_SECRET_PREFIXES.some((p) => name.startsWith(p))) return "reserved";
	return null;
}

/** Turns "tavily api-key" into "TAVILY_API_KEY" as the user types. */
export function normalizeSecretName(raw: string): string {
	return raw
		.toUpperCase()
		.replace(/[\s-]+/g, "_")
		.replace(/[^A-Z0-9_]/g, "");
}