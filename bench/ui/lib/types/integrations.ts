// Integrations area (web parity L19, handlers/integrations.go).

export type WebhookEventType =
	| "evaluation.finished"
	| "evaluation.regressed"
	| "release.blocked"
	| "schedule.failed"
	| "worker.offline"
	| "experiment.finished"
	| "job.failed";

/** What a hook's receiver gets: the signed JSON event, or a Slack, Microsoft Teams or PagerDuty message. */
export type WebhookFormat = "json" | "slack" | "teams" | "pagerduty";

export interface WebhookDelivery {
	id: string;
	hook_id: string;
	event: string;
	event_id: string;
	at: string;
	ok: boolean;
	status_code?: number;
	error?: string;
	attempts: number;
	duration_ms: number;
	/** Delivery log state: retrying rows are queued for another attempt (backoff, up to 24 h). */
	state?: "delivered" | "retrying" | "failed";
	next_attempt_at?: string;
}

export interface Webhook {
	id: string;
	url: string;
	events: WebhookEventType[];
	description?: string;
	enabled: boolean;
	secret_last4: string;
	created_by?: string;
	created_at: string;
	last_delivery?: WebhookDelivery;
	format?: WebhookFormat;
	/** End of a PagerDuty hook's integration key. */
	routing_key_last4?: string;
}

export interface WebhookList {
	webhooks: Webhook[];
	events: WebhookEventType[];
	hosted: boolean;
}

export interface WebhookInput {
	url?: string;
	events?: WebhookEventType[];
	description?: string;
	enabled?: boolean;
	/** Fixed when the hook is created. */
	format?: WebhookFormat;
	/** PagerDuty Events API v2 integration key (pagerduty hooks only). */
	routing_key?: string;
}

/** Events a member can get by email (handlers/notifications.go). */
export type EmailEventType = "evaluation.finished" | "evaluation.regressed" | "release.blocked" | "schedule.failed" | "worker.offline";

export interface EmailPrefs {
	events: EmailEventType[];
	digest: boolean;
	updated_at?: string;
}

/** GET /notifications/email: the caller's own email notifications. */
export interface EmailSettings {
	available: boolean;
	reason?: string;
	/** False while the server has no mail service: emails are only written to its log. */
	delivers: boolean;
	provider: string;
	email?: string;
	events: EmailEventType[];
	prefs: EmailPrefs;
}

export interface EmailTestResult {
	to: string;
	delivers: boolean;
	provider: string;
}

export interface WebhookCreated {
	webhook: Webhook;
	secret: string;
}

export type IntegrationExportFormat = "otlp" | "eee" | "jsonl" | "csv" | "harbor";

export interface IntegrationExport {
	name: string;
	size: number;
	created_at: string;
	format: IntegrationExportFormat;
}

export interface IntegrationExportList {
	exports: IntegrationExport[];
	formats: IntegrationExportFormat[];
}

export interface CIWorkflowPreview {
	path: string;
	mode: string;
	workflow: string;
	/** github or gitlab (hosted mode). */
	provider?: string;
}

/** Query of GET /integrations/ci/workflow. Hosted mode takes the rest. */
export interface CIWorkflowParams {
	mode: string;
	agent?: string;
	provider?: "github" | "gitlab";
	source?: "config" | "image" | "endpoint";
	config?: string;
	url?: string;
	pack?: string;
	benchmarks?: string;
	faults?: string;
	fail_on?: string;
}

/** Params of the integrations.export job. */
export interface IntegrationsExportParams {
	format: IntegrationExportFormat;
	exp_id?: string;
	suite?: string;
	payloads?: boolean;
	allow_synthetic?: boolean;
}
