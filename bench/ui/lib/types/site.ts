// The public site (lane Y08): the routes a visitor reads without signing in
// (handlers/publicsite.go). Field names follow the Go JSON tags.

export interface PublicLibraryTotals {
	benchmarks: number;
	tasks: number;
	ready: number;
	packs: number;
}

/** GET /public/overview. */
export interface PublicOverview {
	product: string;
	version: string;
	commit?: string;
	library: PublicLibraryTotals;
	downloads: boolean;
	mcp: boolean;
	mcp_path?: string;
}

export interface PublicBenchmark {
	id: string;
	title: string;
	description: string;
	track?: string;
	grading?: string;
	licenses: string[];
	source?: string;
	task_count: number;
	ready_count: number;
	held_out_count?: number;
	packs: string[];
	error?: string;
}

export interface PublicPack {
	id: string;
	name: string;
	description: string;
	benchmarks: string[];
}

/** GET /public/benchmarks. */
export interface PublicLibrary {
	available: boolean;
	totals: PublicLibraryTotals;
	benchmarks: PublicBenchmark[];
	packs: PublicPack[];
	updated_at: string;
}

export interface PublicLimit {
	name: string;
	value: string;
}

export interface PublicPlan {
	id: string;
	name: string;
	price: string;
	period: string;
	summary: string;
	features: string[];
	limits: PublicLimit[];
	cta: "signup" | "contact" | string;
}

/** GET /public/pricing. */
export interface PublicPricing {
	beta: boolean;
	headline: string;
	note: string;
	plans: PublicPlan[];
	faq: { q: string; a: string }[];
}

export type DownloadKind = "cli" | "python-wheel" | "python-sdist" | "typescript" | "source";

export interface DownloadFile {
	name: string;
	kind: DownloadKind | string;
	os?: string;
	arch?: string;
	size: number;
	sha256: string;
}

/** GET /public/downloads. */
export interface DownloadsManifest {
	available: boolean;
	commit: string;
	version: string;
	built_at?: string;
	files: DownloadFile[];
	base_url?: string;
	mcp_url?: string;
	gitlab_url?: string;
}
