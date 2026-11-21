/// <reference types="vite/client" />

interface ImportMetaEnv {
	/** "1" makes RTK Query read public/fixtures/*.json instead of calling /api/bench. */
	readonly VITE_BENCH_FIXTURE?: string;
}

interface ImportMeta {
	readonly env: ImportMetaEnv;
}
/**
 * Whether fixture mode is compiled in: true for the dev server (and a build made with
 * VITE_BENCH_FIXTURE=1), false for every production build, so ?fixture=1 does nothing there
 * and the fixture base query is dropped from the bundle (vite.config.mts define).
 */
declare const __BENCH_FIXTURES__: boolean;
