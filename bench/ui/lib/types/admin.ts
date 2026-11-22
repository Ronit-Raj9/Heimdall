// Server admin page (web parity L23): platform routes under /api/bench/admin/.
// Users, orgs, quotas, kill switch and containers are served by handlers/serveradmin.go;
// image approval by the shared pool area (L03) and usage by the usage area (L25).

export interface AdminUser {
	id: string;
	email: string;
	name: string;
	disabled: boolean;
	github: boolean;
	orgs: number;
	created_at: string;
	server_admin: boolean;
}

export interface AdminLimits {
	max_concurrent_runs: number;
	max_runs_per_day: number;
	max_spend_usd_per_day: number;
	max_image_bytes: number;
	allow_custom_images: boolean;
	shared_pool: boolean;
	runtime: "runc" | "runsc";
}

export interface AdminOrg {
	id: string;
	slug: string;
	name: string;
	public_read: boolean;
	members: number;
	created_at: string;
	quota?: AdminLimits;
}

export interface AdminQuota {
	org_id: string;
	limits: AdminLimits;
	defaults: AdminLimits;
	/** false: the server's quota store is the in-memory stub, so edits do not survive a restart. */
	persisted: boolean;
}

export interface AdminContainer {
	org_id: string;
	org_slug: string;
	/** The organization's display name (N11). */
	org_name?: string;
	runner_id: string;
	runner: string;
	pool: string;
	host: string;
	state: string;
	live: boolean;
	slots: number;
	running: string[];
	last_seen: string;
}

export interface AdminOverview {
	users: number;
	orgs: number;
	running_slots: number;
	kill_switch: boolean;
	quota_persisted: boolean;
}

/** A custom image an org asked to use (L03 GET /admin/images). Fields beyond id/ref/status are optional. */
/** One image request (sharedpool.ImageRequest): GET /admin/images. */
export interface AdminImage {
	id: string;
	org_id: string;
	image: string;
	size_bytes: number;
	/** The org's note to the admin. */
	note?: string;
	status: "pending" | "approved" | "rejected";
	requested_by?: string;
	decided_by?: string;
	/** The admin's note on approve or reject. */
	decision?: string;
	created_at: string;
	decided_at?: string;
}

/** One organization's row of GET /admin/usage (handlers.AdminOrgUsage). */
export interface AdminUsageRow {
	org_id: string;
	slug?: string;
	name?: string;
	cost_usd: number;
	tokens?: number;
	runs: number;
	/** Runs whose model price is not known: their cost is not in cost_usd. */
	unknown_cost_runs?: number;
	keys_month_spent_usd?: number;
	/** Why this organization's runs could not be read. */
	error?: string;
}
/** GET /admin/usage (handlers.AdminUsageReport). */
export interface AdminUsage {
	from: string;
	to: string;
	total_cost_usd: number;
	total_tokens?: number;
	runs: number;
	orgs: AdminUsageRow[];
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		admin: typeof import("@/locales/en/admin.json");
	}
}