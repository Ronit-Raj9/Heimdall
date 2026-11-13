// i18n first: it initialises synchronously, so the first render already has English strings.
import "@/lib/i18n";

// Tailwind + design tokens.
import "@/app/globals.css";

import { RouterProvider, createRouter, parseSearchWith, stringifySearchWith } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { NotFoundComponent } from "./__notFound";
import { routeTree } from "./routeTree.gen";

// Copied from ui/app/main.tsx so query params stay compatible with nuqs parsers: only JSON.parse
// objects and arrays; plain strings and numbers stay raw so ids are never coerced.
function safeJsonParse(value: string): unknown {
	const c = value[0];
	if (c === "{" || c === "[") {
		try {
			return JSON.parse(value);
		} catch {
			return value;
		}
	}
	return value;
}

function stringifySearchValue(value: unknown): string {
	if (Array.isArray(value)) return value.map(String).join(",");
	if (value === null || value === undefined) return "";
	if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
		return String(value);
	}
	return JSON.stringify(value);
}

const router = createRouter({
	routeTree,
	defaultPreload: "intent",
	scrollRestoration: true,
	notFoundMode: "root",
	defaultNotFoundComponent: NotFoundComponent,
	parseSearch: parseSearchWith(safeJsonParse),
	stringifySearch: stringifySearchWith(stringifySearchValue),
});

declare module "@tanstack/react-router" {
	interface Register {
		router: typeof router;
	}
}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("Root element #root not found");

createRoot(rootEl).render(
	<StrictMode>
		<RouterProvider router={router} />
	</StrictMode>,
);