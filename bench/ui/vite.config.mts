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

export default defineConfig({
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
});