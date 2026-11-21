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
}

export interface CatalogListResponse {
	entries: CatalogEntry[];
	source_available: boolean;
	starter_agents: string[];
}

export interface CatalogAddRequest {
	id: string;
	from?: string;
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
