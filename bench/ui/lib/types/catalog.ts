// Benchmark catalog (lane L12): GET /benchmarks/catalog, POST /benchmarks/catalog/{id}/add.

export type CatalogKind = "example" | "bundled" | "importer";

export interface CatalogEntry {
	id: string;
	title: string;
	kind: CatalogKind;
	summary: string;
	track?: string;
	grading?: string;
	license?: string;
	source?: string;
	benchmarks?: string[];
	needs?: string[];
	tags?: string[];
	importer?: string;
	suite_id?: string;
	suites?: string[];
	agents?: string[];
	extra?: Record<string, string>;
	available: boolean;
	installed: boolean;
	needs_data: boolean;
	hint?: string;
	/** What "Upload dataset" takes (zip or the importer's own file). */
	upload_expects?: string;
	/** "Fetch from URL" can work (public data). */
	fetch_allowed?: boolean;
	/** Known public URL to prefill. */
	fetch_url?: string;
	/** Extra step the import needs (e.g. set ref=<commit>). */
	data_note?: string;
	/** Project path the data lands in. */
	dataset_path?: string;
}

export interface CatalogListResponse {
	entries: CatalogEntry[];
	source_available: boolean;
	starter_agents: string[];
}

export interface CatalogAddRequest {
	id: string;
	from?: string;
	/** Public https URL the server fetches instead of from. */
	url?: string;
	set?: Record<string, string>;
	suite?: string;
	limit?: number;
}

export interface CatalogAddResult {
	entry: string;
	suites: string[] | null;
	agents: string[] | null;
	written: string[] | null;
	skipped: string[] | null;
}

export interface CatalogAddResponse {
	entry: string;
	kind: CatalogKind;
	result?: CatalogAddResult;
	job?: { id: string; kind: string; status: string };
}

/** POST /benchmarks/catalog/{id}/dataset answer. */
export interface CatalogDatasetResponse {
	entry: string;
	from: string;
	size: number;
	zip: boolean;
}
