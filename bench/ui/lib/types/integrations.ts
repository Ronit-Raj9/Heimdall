// Integrations area (web parity L19, handlers/integrations.go).

export type WebhookEventType = "experiment.finished" | "job.failed";

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
