// Lane Y15: annotation queues, benchmark data (rows as versions), platform imports and the
// benchmark data policy. Shapes follow bench/handlers/annotation.go, datasetapi.go,
// platformimport.go and benchmarkaccess.go.

export type AnnotationLabel = "correct" | "incorrect";
export type AnnotationKind = "runs" | "judge";
export type AnnotationAssignment = "open" | "fixed";
export type AnnotationState = "open" | "labelling" | "disputed" | "done";

export interface AnnotationQueue {
	id: string;
	name: string;
	description?: string;
	instructions?: string;
	benchmark?: string;
	kind: AnnotationKind;
	reviewers_per_item: number;
	blind: boolean;
	assignment: AnnotationAssignment;
	reviewers?: string[];
	created_by: string;
	created_by_email?: string;
	created_at: string;
	updated_at: string;
	archived?: boolean;
	counts: AnnotationCounts;
	mine: number;
	can_manage: boolean;
	can_label: boolean;
}

export interface AnnotationCounts {
	total: number;
	open: number;
	labelling: number;
	disputed: number;
	done: number;
	calibrated: number;
}

export interface AnnotationQueueList {
	queues: AnnotationQueue[] | null;
}

export interface AnnotationQueueRequest {
	name?: string;
	description?: string;
	instructions?: string;
	benchmark?: string;
	kind?: AnnotationKind;
	reviewers_per_item?: number;
	blind?: boolean;
	assignment?: AnnotationAssignment;
	reviewers?: string[];
	archived?: boolean;
}

export interface AnnotationItemsRequest {
	experiment?: string;
	sample?: number;
	stratify?: boolean;
}

export interface AnnotationItemsResult {
	added: number;
	duplicate: number;
	skipped: string[] | null;
}

export interface AnnotationLabelEntry {
	user_id: string;
	email?: string;
	label: AnnotationLabel;
	note?: string;
	at: string;
}

export interface AnnotationResolution {
	label: AnnotationLabel;
	method: "consensus" | "decided";
	user_id?: string;
	email?: string;
	note?: string;
	at: string;
}

export interface AnnotationItem {
	id: string;
	state: AnnotationState;
	task: string;
	question?: string;
	answer: string;
	/** Where a run's answer was read (N72): the answer file, the files the agent wrote, or its final message. */
	answer_from?: AnnotationAnswerFrom;
	run_id?: string;
	experiment?: string;
	agent?: string;
	agent_version?: string;
	hidden?: string[];
	judge_label?: AnnotationLabel | "";
	judge_votes?: string[];
	judge_reason?: string;
	labels: AnnotationLabelEntry[] | null;
	label_count: number;
	my_label?: AnnotationLabel | "";
	resolution?: AnnotationResolution | null;
	calibration?: string;
	calibration_error?: string;
	assigned?: string[];
	added_at: string;
}

export interface AnnotationItemList {
	queue: AnnotationQueue;
	items: AnnotationItem[] | null;
	total: number;
}

export interface AnnotationNext {
	queue: AnnotationQueue;
	item: AnnotationItem | null;
}

export interface AnnotationLabelResult {
	item: AnnotationItem;
	resolved: boolean;
	calibration?: string;
	calibration_error?: string;
}

export interface PairKappa {
	a: string;
	b: string;
	items: number;
	kappa: number | null;
	agree: number;
}

export type AnnotationAnswerFrom = "answer" | "files" | "final_message";

/** One member who can be named as a labeller: GET /annotation/people. */
export interface AnnotationPerson {
	user_id: string;
	name: string;
}

/** GET /annotation/people: members by display name; `local` on a server with no members list. */
export interface AnnotationPeople {
	people: AnnotationPerson[];
	local?: boolean;
}

export interface AnnotationAgreement {
	queue: string;
	agreement: {
		multi_labelled: number;
		unanimous: number;
		percent_agreement: number | null;
		cohen: PairKappa[] | null;
		cohen_mean: number | null;
		fleiss: number | null;
		fleiss_items: number;
		interpretation?: string;
		judge?: {
			items: number;
			agree: number;
			rate: number | null;
			kappa: number | null;
			false_passes: number;
			false_fails: number;
		} | null;
	};
	labellers: Record<string, string> | null;
}

// Benchmark data: rows as versions, data policy, platform imports.

export interface DatasetVersion {
	version: number;
	at: string;
	actor?: string;
	source?: string;
	added: string[] | null;
	updated: string[] | null;
	removed: string[] | null;
	tasks: number;
	sha256: string;
}

export interface DatasetVersionList {
	benchmark: string;
	versions: DatasetVersion[] | null;
}

export type PIIMode = "off" | "warn" | "mask" | "redact" | "block";
export type AccessMode = "org" | "restricted";

export interface DataPolicy {
	pii: { mode: PIIMode; kinds?: string[] | null };
	access: { mode: AccessMode; users?: string[] | null };
	updated_by?: string;
	updated_at?: string;
}

export interface DataPolicyView {
	benchmark: string;
	policy: DataPolicy;
	can_edit_access: boolean;
	modes: PIIMode[] | null;
	kinds: string[] | null;
}

export interface DataPolicyRequest {
	pii?: { mode: PIIMode; kinds?: string[] };
	access?: { mode: AccessMode; users?: string[] };
}

export interface PlatformSecretSpec {
	role: string;
	label: string;
	default: string;
}

export interface ImportPlatform {
	id: string;
	name: string;
	base_url: string;
	kinds: string[] | null;
	secrets: PlatformSecretSpec[] | null;
	dataset_hint: string;
	project_hint: string;
	docs: string;
	configured: Record<string, boolean> | null;
}

export interface ImportPlatformList {
	platforms: ImportPlatform[] | null;
	secrets: string[] | null;
	secrets_link: string;
}

export interface PlatformImportRequest {
	platform: string;
	kind: "dataset" | "traces";
	target: "benchmark" | "inbox";
	benchmark?: string;
	base_url?: string;
	dataset?: string;
	project?: string;
	query?: string;
	limit?: number;
	secrets?: Record<string, string>;
	dry_run?: boolean;
}

export interface PIIReport {
	mode: string;
	kinds: Record<string, number> | null;
	rows: number;
	findings: { index: number; id?: string; fields: string[] | null; kinds: Record<string, number> | null }[] | null;
	summary: string;
	unscanned?: string[] | null;
}

export interface DatasetWriteResult {
	benchmark: string;
	dry_run: boolean;
	version: DatasetVersion | null;
	added: string[] | null;
	updated: string[] | null;
	unchanged: string[] | null;
	problems: { index: number; id?: string; message: string }[] | null;
	pii: PIIReport | null;
	next: string;
}

export interface PlatformImportResult {
	platform: string;
	kind: string;
	target: string;
	read: number;
	truncated: boolean;
	warnings: string[] | null;
	preview: { source_id: string; input: string; expected?: string; chat?: boolean }[] | null;
	benchmark?: DatasetWriteResult | null;
	inbox?: { created: number; repeated: number; items: string[] | null; problems: string[] | null } | null;
	next: string;
}