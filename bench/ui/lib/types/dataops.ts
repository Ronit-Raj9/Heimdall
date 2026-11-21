// Data operations (lane X16): deletes, data retention, webhook delivery log, organization
// deletion and the audit log export and integrity check.

import type { WebhookDelivery } from "./integrations";

/** DELETE /experiments/{id} and DELETE /evaluations/{id}. */
export interface DeletedResponse {
	deleted: string;
	kind: "experiment" | "evaluation";
	/** For an evaluation: its benchmark comparisons, deleted with it. */
	experiments?: string[];
}

/** GET and PUT /data/retention. run_days 0 keeps everything forever. */
export interface RetentionPolicy {
	run_days: number;
	min_days: number;
	max_days: number;
	last_sweep_at?: string;
	last_deleted?: number;
}

/** GET /integrations/deliveries. */
export interface DeliveryList {
	deliveries: WebhookDelivery[];
}

/** POST /orgs/{org}/delete. */
export interface OrgDeleteResponse {
	org_id: string;
	slug: string;
	status: "scheduled";
	requested_by: string;
	requested_at: string;
}

/** GET /orgs/{org}/audit/verify. */
export interface AuditVerifyResult {
	ok: boolean;
	entries: number;
	chained: number;
	legacy: number;
	head_seq: number;
	head_hash: string;
	broken_at: number;
	reason: string;
}

export type AuditExportFormat = "jsonl" | "csv";
