/// <reference types="vite/client" />

interface ImportMetaEnv {
	/** "1" makes RTK Query read public/fixtures/*.json instead of calling /api/bench. */
	readonly VITE_BENCH_FIXTURE?: string;
}

interface ImportMeta {
	readonly env: ImportMetaEnv;
}