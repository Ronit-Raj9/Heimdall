import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
	plugins: [react()],
	resolve: {
		alias: {
			"@": path.resolve(__dirname),
		},
	},
	test: {
		// Components render through react-dom/server, so tests need no DOM library.
		environment: "node",
		include: ["**/*.test.{ts,tsx}"],
		exclude: ["node_modules/**", "dist/**"],
	},
});