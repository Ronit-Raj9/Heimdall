import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const outDir = path.resolve(__dirname, "dist");

// emptyOutDir wipes dist/, including the committed placeholder that lets `//go:embed all:dist`
// compile before the first UI build. Put it back so a build leaves the git tree unchanged.
function keepGitkeep(): Plugin {
	return {
		name: "bench-keep-gitkeep",
		apply: "build",
		closeBundle() {
			fs.mkdirSync(outDir, { recursive: true });
			fs.writeFileSync(path.join(outDir, ".gitkeep"), "");
		},
	};
}

// Fixture mode (lib/store/apis/fixtures.ts) is for building screens without a server: on for
// the dev server, off for a production build unless VITE_BENCH_FIXTURE=1 is set at build time.
// Off, __BENCH_FIXTURES__ is the literal false (the fixture code is dropped) and the copied
// public/fixtures folder is removed from dist, so no install ever ships fixture data.
function dropFixtures(enabled: boolean): Plugin {
	let dir = outDir;
	return {
		name: "bench-drop-fixtures",
		apply: "build",
		configResolved(config) {
			dir = path.resolve(config.root, config.build.outDir);
		},
		closeBundle() {
			if (!enabled) fs.rmSync(path.join(dir, "fixtures"), { recursive: true, force: true });
		},
	};
}

export default defineConfig(({ command }) => {
	const fixtures = command === "serve" || process.env.VITE_BENCH_FIXTURE === "1";
	return {
		define: { __BENCH_FIXTURES__: JSON.stringify(fixtures) },
		plugins: [
			tanstackRouter({
				target: "react",
				routesDirectory: "./app",
				generatedRouteTree: "./app/routeTree.gen.ts",
				// Same convention as ui/: routes live in layout.tsx, page.tsx is composition only,
				// so a screen folder can move into ui/app/workspace/benchmarks/ unchanged.
				routeToken: "layout",
				routeFileIgnorePattern: "^(?!layout\\.tsx$|__root\\.tsx$).+\\.(tsx|ts|jsx|js)$",
				autoCodeSplitting: true,
			}),
			react(),
			tailwindcss(),
			keepGitkeep(),
			dropFixtures(fixtures),
		],
		resolve: {
			alias: {
				"@": path.resolve(__dirname),
			},
		},
		server: {
			port: 3001,
			proxy: {
				"/api/bench": {
					target: "http://127.0.0.1:8090",
					changeOrigin: true,
				},
			},
		},
		build: {
			outDir,
			emptyOutDir: true,
		},
	};
});
