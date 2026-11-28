import type { GwEffect } from "@/lib/types/gateway";

/** gwfeature overheadBucket: one part of Bifrost's own time. */
export interface OverheadBucket {
	name: string;
	rows: number;
	median_us: number;
	p95_us: number;
	share: number;
}

/** gwfeature overheadDetails, the Details of GET /experiments/{id}/gateway-overhead. */
export interface OverheadDetails {
	rows: number;
	bucket_rows: number;
	unknown_rows: number;
	median_ms?: number;
	p95_ms?: number;
	buckets: OverheadBucket[];
	no_time: string[];
	gateway?: string;
}

export type OverheadEffect = Omit<GwEffect, "details"> & { details?: OverheadDetails };

/** schemas.Review, one line of a comparison's review log. */
export interface ReviewLogEntry {
	review_id: string;
	exp_id: string;
	run_id: string;
	decision: string;
	reason: string;
	reviewer: string;
	created_at: string;
	supersedes?: string;
	hash: string;
}

/** schemas.ReviewLog: GET /experiments/{id}/reviews. */
export interface ReviewLog {
	exp_id: string;
	reviews: ReviewLogEntry[] | null;
	verified: boolean;
	problems: string[] | null;
}