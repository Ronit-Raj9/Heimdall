/** Shared runner pool and guardrails (web parity lane L03). Mirrors bench/quota and bench/sharedpool. */

export type RunnerRuntime = "runc" | "runsc";

export interface OrgLimits {
	max_concurrent_runs: number;
	max_runs_per_day: number;
	max_spend_usd_per_day: number;
	max_image_bytes: number;
	allow_custom_images: boolean;
	shared_pool: boolean;
	runtime: RunnerRuntime;
}

export interface PoolStatus {
	shared_pool_configured: boolean;
	kill_switch: boolean;
	limits: OrgLimits;
	message: string;
}

export type ImageRequestStatus = "pending" | "approved" | "rejected";

export interface ImageRequest {
	id: string;
	org_id: string;
	image: string;
	size_bytes: number;
	note?: string;
	status: ImageRequestStatus;
	requested_by?: string;
	decided_by?: string;
	decision?: string;
	created_at: string;
	decided_at?: string;
}

export interface ImageRequestList {
	images: ImageRequest[];
}

export interface ImageRequestBody {
	image: string;
	size_bytes: number;
	note?: string;
}

export interface OrgQuota {
	org_id: string;
	limits: OrgLimits;
}

export interface KillSwitch {
	on: boolean;
}

/** A digest-pinned image reference: name[:tag]@sha256:<64 hex>. Mirrors sharedpool.CheckImageRef. */
export const PINNED_IMAGE_RE = /^[a-z0-9]+([._/:-][a-z0-9]+)*@sha256:[a-f0-9]{64}$/;

export function isPinnedImage(ref: string): boolean {
	const s = ref.trim();
	return s.length <= 512 && PINNED_IMAGE_RE.test(s);
}
